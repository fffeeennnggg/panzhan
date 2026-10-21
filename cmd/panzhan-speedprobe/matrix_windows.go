// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"panzhan/internal/download"
	"panzhan/internal/quark"
)

// Set to 64 only when building with the documented experimental Go overlay.
var probeWorkerLimit = "8"

func matrixHTTPTransport(template *http.Transport, protocol string) *http.Transport {
	base := template.Clone()
	if protocol != "auto" {
		// Clone preserves ALPN populated by earlier account API requests.
		// Protocols alone does not clear it when HTTP/2 is disabled.
		base.TLSNextProto = nil
		if base.TLSClientConfig == nil {
			base.TLSClientConfig = &tls.Config{}
		}
		base.TLSClientConfig.NextProtos = []string{"h2"}
		if protocol == "h1" {
			base.TLSClientConfig.NextProtos = []string{"http/1.1"}
		}
		base.Protocols = new(http.Protocols)
		base.Protocols.SetHTTP1(protocol == "h1")
		base.Protocols.SetHTTP2(protocol == "h2")
	}
	return base
}

type matrixConfig struct {
	Workers  int    `json:"workers"`
	ChunkMiB int64  `json:"chunk_mib"`
	Protocol string `json:"protocol"`
}

type matrixTransport struct {
	base         *http.Transport
	meter        *meter
	mu           sync.Mutex
	connections  map[net.Conn]bool
	statuses     map[int]int
	ranges       map[string]int
	active, peak int
}

func (t *matrixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.ranges[req.Header.Get("Range")]++
	t.mu.Unlock()
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		t.mu.Lock()
		t.connections[info.Conn] = true
		t.mu.Unlock()
	}}
	t.meter.requests.Add(1)
	res, err := t.base.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	if err != nil {
		return nil, err
	}
	t.meter.status.Store(int64(res.StatusCode))
	t.meter.protocol.Store(res.Proto)
	t.mu.Lock()
	t.statuses[res.StatusCode]++
	t.active++
	t.peak = max(t.peak, t.active)
	t.mu.Unlock()
	res.Body = &matrixBody{ReadCloser: &measuredBody{ReadCloser: res.Body, meter: t.meter}, owner: t}
	return res, nil
}

type matrixBody struct {
	io.ReadCloser
	owner *matrixTransport
	once  sync.Once
}

func (b *matrixBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.owner.mu.Lock(); b.owner.active--; b.owner.mu.Unlock() })
	return err
}

type matrixRun struct {
	config    matrixConfig
	meter     *meter
	transport *matrixTransport
	cancel    context.CancelFunc
	finished  chan struct{}
	root      *os.Root
	dir       string
	mu        sync.Mutex
	done      bool
	err       error
}

func (r *matrixRun) stop() {
	r.cancel()
	<-r.finished
	r.transport.base.CloseIdleConnections()
	r.root.Close()
	os.RemoveAll(r.dir)
}

func runMatrix(link quark.Link, file savedFile, scratch string, maxMiB int64) error {
	limit, err := strconv.Atoi(probeWorkerLimit)
	if err != nil || (limit != 8 && limit != 64) {
		return errors.New("invalid experimental build limit")
	}
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return errors.New("cannot create matrix scratch")
	}
	encoder := json.NewEncoder(os.Stdout)
	u, _ := url.Parse(link.URL)
	encoder.Encode(map[string]any{"event": "ready", "file_size": file.Size, "cdn_host": u.Hostname(), "engine_worker_limit": limit, "traffic_cap_mib": maxMiB})
	var current *matrixRun
	defer func() {
		if current != nil {
			current.stop()
		}
	}()
	snapshot := func(event string) {
		if current == nil {
			encoder.Encode(map[string]any{"event": event})
			return
		}
		r := current
		r.mu.Lock()
		done := r.done
		failure := ""
		if r.err != nil && !errors.Is(r.err, context.Canceled) {
			failure = fmt.Sprintf("download failed (%T)", r.err)
		}
		r.mu.Unlock()
		r.transport.mu.Lock()
		statuses := make(map[int]int)
		for k, v := range r.transport.statuses {
			statuses[k] = v
		}
		connections, peak, active := len(r.transport.connections), r.transport.peak, r.transport.active
		repeatedRanges := 0
		for _, count := range r.transport.ranges {
			repeatedRanges += count - 1
		}
		r.transport.mu.Unlock()
		encoder.Encode(map[string]any{"event": event, "config": r.config, "received": r.meter.received.Load(), "requests": r.meter.requests.Load(), "protocol": r.meter.protocol.Load(), "statuses": statuses, "connections": connections, "peak_active_responses": peak, "active_responses": active, "repeated_ranges": repeatedRanges, "done": done, "error": failure})
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		command := scanner.Text()
		if strings.HasPrefix(command, "start ") {
			if current != nil {
				return errors.New("stop previous case before starting")
			}
			var config matrixConfig
			if json.Unmarshal([]byte(strings.TrimPrefix(command, "start ")), &config) != nil || config.Workers < 1 || config.Workers > limit || config.ChunkMiB < 1 || config.ChunkMiB > 32 || (config.Protocol != "auto" && config.Protocol != "h1" && config.Protocol != "h2") {
				return errors.New("invalid matrix configuration")
			}
			dir, err := os.MkdirTemp(scratch, "matrix-")
			if err != nil {
				return errors.New("cannot create matrix directory")
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				os.RemoveAll(dir)
				return errors.New("cannot open matrix directory")
			}
			ctx, cancel := context.WithCancel(context.Background())
			m := &meter{cancel: cancel, limit: maxMiB << 20}
			m.protocol.Store("")
			base := matrixHTTPTransport(http.DefaultTransport.(*http.Transport), config.Protocol)
			t := &matrixTransport{base: base, meter: m, connections: make(map[net.Conn]bool), statuses: make(map[int]int), ranges: make(map[string]int)}
			r := &matrixRun{config: config, meter: m, transport: t, cancel: cancel, finished: make(chan struct{}), root: root, dir: dir}
			current = r
			go func() {
				engine := download.Engine{HTTP: &http.Client{Transport: t, Timeout: 10 * time.Minute}, Concurrency: config.Workers, ChunkSize: config.ChunkMiB << 20}
				err := engine.Fetch(ctx, root, "sample.bin", "direct-download-matrix", file.Size, "", func(context.Context) (download.Link, error) {
					return download.Link{URL: link.URL, Header: link.Header}, nil
				}, nil)
				r.mu.Lock()
				r.err = err
				r.done = true
				r.mu.Unlock()
				close(r.finished)
			}()
			snapshot("started")
			continue
		}
		switch command {
		case "sample":
			snapshot("sample")
		case "stop":
			if current == nil {
				return errors.New("no active matrix case")
			}
			current.stop()
			snapshot("stopped")
			current = nil
		case "quit":
			return nil
		default:
			return errors.New("unknown matrix command")
		}
	}
	return scanner.Err()
}
