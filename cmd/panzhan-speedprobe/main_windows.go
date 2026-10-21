// SPDX-License-Identifier: AGPL-3.0-or-later
// Experimental Windows-only runner. It uses the production download engine;
// OpenSpeedy is never loaded by the normal application or the fnOS package.
package main

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"panzhan/internal/download"
	"panzhan/internal/quark"
)

type savedFile struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

type savedState struct {
	CookieCipher string `json:"cookie_cipher"`
	Jobs         []struct {
		Status string      `json:"status"`
		Files  []savedFile `json:"files"`
	} `json:"jobs"`
}

type meter struct {
	received     atomic.Int64
	requests     atomic.Int64
	status       atomic.Int64
	protocol     atomic.Value
	responseInfo atomic.Value
	cancel       context.CancelFunc
	limit        int64
}

type measuredTransport struct{ meter *meter }

func (t measuredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.meter.requests.Add(1)
	res, err := http.DefaultTransport.RoundTrip(req)
	if err == nil {
		t.meter.status.Store(int64(res.StatusCode))
		t.meter.protocol.Store(res.Proto)
		if res.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			lower := strings.ToLower(string(body))
			t.meter.responseInfo.Store(map[string]any{"server": res.Header.Get("Server"), "content_type": res.Header.Get("Content-Type"), "captcha_marker": strings.Contains(lower, "captcha"), "signature_marker": strings.Contains(lower, "signature"), "access_denied_marker": strings.Contains(lower, "accessdenied"), "precondition_marker": strings.Contains(lower, "precondition")})
			res.Body.Close()
			res.Body = io.NopCloser(strings.NewReader(""))
		}
		res.Body = &measuredBody{ReadCloser: res.Body, meter: t.meter}
	}
	return res, err
}

type measuredBody struct {
	io.ReadCloser
	meter *meter
}

func (b *measuredBody) Read(p []byte) (int, error) {
	// Bound traffic even if the independent controller stops responding.
	n, err := b.ReadCloser.Read(p[:min(len(p), 32<<10)])
	if b.meter.received.Add(int64(n)) >= b.meter.limit {
		b.meter.cancel()
	}
	return n, err
}

func readAccount(dataDir string) (string, savedFile, error) {
	var state savedState
	b, err := os.ReadFile(filepath.Join(dataDir, "state.json"))
	if err != nil {
		return "", savedFile{}, errors.New("cannot read existing state")
	}
	if json.Unmarshal(b, &state) != nil {
		return "", savedFile{}, errors.New("invalid existing state")
	}
	var selected savedFile
	for _, job := range state.Jobs {
		if job.Status == "queued" || job.Status == "downloading" || job.Status == "transferring" || job.Status == "verifying" {
			return "", selected, errors.New("existing local tasks are active; stop them before probing")
		}
		if job.Status == "completed" {
			for _, f := range job.Files {
				if f.ID != "" && f.Size > selected.Size {
					selected = f
				}
			}
		}
	}
	if selected.Size < 256<<20 {
		return "", selected, errors.New("no completed cloud file larger than 256 MiB")
	}
	key, err := os.ReadFile(filepath.Join(dataDir, "master.key"))
	if err != nil {
		return "", selected, errors.New("cannot read account key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", selected, errors.New("invalid account key")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", selected, errors.New("cannot initialize account decryption")
	}
	sealed, err := base64.StdEncoding.DecodeString(state.CookieCipher)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return "", selected, errors.New("no usable saved account")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte("panzhan-cookie-v1"))
	if err != nil {
		return "", selected, errors.New("cannot decrypt saved account")
	}
	return string(plain), selected, nil
}

func run() error {
	dataDir := flag.String("data", "data", "Existing account data, read only")
	output := flag.String("scratch", ".tmp/openspeedy-probe", "Parent of disposable download directory")
	patch := flag.String("speedpatch", "", "Optional absolute path to official speedpatch64.dll")
	factor := flag.Float64("factor", 1, "OpenSpeedy multiplier (1 through 100)")
	maxMiB := flag.Int64("max-mib", 128, "Total traffic cap (128 through 512 MiB)")
	clockOnly := flag.Bool("clock-only", false, "Verify hook activation without cloud access")
	verify := flag.Bool("verify-account", false, "Verify saved account in memory before resolving")
	matrix := flag.Bool("matrix", false, "Compare download strategies with the same in-memory cloud link")
	flag.Parse()
	if *factor < 1 || *factor > 100 || math.IsNaN(*factor) {
		return errors.New("factor must be between 1 and 100")
	}
	if *maxMiB < 128 || *maxMiB > 512 {
		return errors.New("traffic cap must be between 128 and 512 MiB")
	}
	if *patch == "" && *factor != 1 {
		return errors.New("non-default factor requires a DLL")
	}

	var link quark.Link
	var file savedFile
	if !*clockOnly {
		cookie, selected, err := readAccount(*dataDir)
		if err != nil {
			return err
		}
		file = selected
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		// Resolve before installing time hooks. Never persist refreshed cookies.
		client := quark.New(cookie, nil)
		if *verify {
			if err := client.Verify(ctx); err != nil {
				return fmt.Errorf("verify existing account: %w", err)
			}
		}
		link, err = client.DownloadLink(ctx, file.ID)
		if err != nil {
			return fmt.Errorf("resolve existing cloud file: %w", err)
		}
	}
	if *matrix {
		if *clockOnly || *patch != "" {
			return errors.New("matrix mode cannot use time hooks or clock-only mode")
		}
		return runMatrix(link, file, *output, *maxMiB)
	}

	kernel := syscall.NewLazyDLL("kernel32.dll")
	tick := kernel.NewProc("GetTickCount64")
	fileTime := kernel.NewProc("GetSystemTimeAsFileTime")
	if err := tick.Find(); err != nil {
		return errors.New("cannot locate Windows tick clock")
	}
	if err := fileTime.Find(); err != nil {
		return errors.New("cannot locate Windows wall clock")
	}
	var hookFailures any
	var setSpeed *syscall.Proc
	patchAPI := "none"
	if *patch != "" {
		if !filepath.IsAbs(*patch) {
			return errors.New("speedpatch path must be absolute")
		}
		dll, err := syscall.LoadDLL(*patch)
		if err != nil {
			return errors.New("cannot load OpenSpeedy DLL")
		}
		// Keep the DLL mapped until process exit; unloading live time hooks is unsafe.
		setSpeed, err = dll.FindProc("SP_SetSpeed")
		if err != nil {
			// Official v1.7.7 uses ChangeSpeed/GetStatus. Its DllMain
			// already calls Init; calling Init twice would leak its mapping.
			setSpeed, err = dll.FindProc("ChangeSpeed")
			if err != nil {
				return errors.New("missing OpenSpeedy speed control")
			}
			patchAPI = "legacy"
		} else {
			patchAPI = "SP"
		}
		enabledName := "SP_IsEnabled"
		if patchAPI == "legacy" {
			enabledName = "GetStatus"
		}
		enabled, err := dll.FindProc(enabledName)
		if err != nil {
			return errors.New("missing OpenSpeedy enabled check")
		}
		setSpeed.Call(uintptr(math.Float64bits(*factor)))
		on, _, _ := enabled.Call()
		if failures, err := dll.FindProc("SP_GetHookFailures"); err == nil {
			count, _, _ := failures.Call()
			hookFailures = count
			if count != 0 {
				return errors.New("OpenSpeedy reported hook failures")
			}
		}
		if on == 0 {
			return errors.New("OpenSpeedy hooks did not activate cleanly")
		}
	}

	if err := os.MkdirAll(*output, 0700); err != nil {
		return errors.New("cannot create scratch parent")
	}
	dir, err := os.MkdirTemp(*output, "engine-")
	if err != nil {
		return errors.New("cannot create isolated download directory")
	}
	defer os.RemoveAll(dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return errors.New("cannot open isolated download directory")
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &meter{cancel: cancel, limit: *maxMiB << 20}
	m.protocol.Store("")
	m.responseInfo.Store(map[string]any{})
	var resultMu sync.Mutex
	var fetchErr error
	var done, started bool
	finished := make(chan struct{})
	engine := download.Engine{HTTP: &http.Client{Transport: measuredTransport{m}, Timeout: 10 * time.Minute}, ChunkSize: 10 << 20, Concurrency: 3}
	encoder := json.NewEncoder(os.Stdout)
	u, _ := url.Parse(link.URL)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	encoder.Encode(map[string]any{"event": "ready", "factor": *factor, "patched": *patch != "", "patch_api": patchAPI, "hook_failures": hookFailures, "file_size": file.Size, "cdn_host": host})
	goStart := time.Now()
	snapshot := func(event string) {
		winTick, _, _ := tick.Call()
		var ft syscall.Filetime
		fileTime.Call(uintptr(unsafe.Pointer(&ft)))
		resultMu.Lock()
		defer resultMu.Unlock()
		failure := ""
		if fetchErr != nil && !errors.Is(fetchErr, context.Canceled) {
			failure = fetchErr.Error()
		}
		encoder.Encode(map[string]any{"event": event, "received": m.received.Load(), "requests": m.requests.Load(), "http_status": m.status.Load(), "response_info": m.responseInfo.Load(), "protocol": m.protocol.Load(), "done": done, "error": failure, "win_tick_ms": winTick, "win_filetime_100ns": uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime), "go_elapsed_ns": time.Since(goStart).Nanoseconds()})
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "speed ") {
			next, err := strconv.ParseFloat(strings.TrimPrefix(scanner.Text(), "speed "), 64)
			if err != nil || next < 1 || next > 100 || math.IsNaN(next) || setSpeed == nil {
				return errors.New("invalid speed command")
			}
			setSpeed.Call(uintptr(math.Float64bits(next)))
			snapshot("speed_changed")
			continue
		}
		switch scanner.Text() {
		case "start":
			if !started {
				started = true
				if !*clockOnly {
					go func() {
						err := engine.Fetch(ctx, root, "sample.bin", "openspeedy-isolated-probe", file.Size, "", func(context.Context) (download.Link, error) {
							return download.Link{URL: link.URL, Header: link.Header}, nil
						}, nil)
						resultMu.Lock()
						fetchErr = err
						done = true
						resultMu.Unlock()
						close(finished)
					}()
				}
			}
			snapshot("started")
		case "sample":
			snapshot("sample")
		case "stop":
			cancel()
			if started && !*clockOnly {
				<-finished
			}
			snapshot("stopped")
			return nil
		default:
			return errors.New("unknown controller command")
		}
	}
	cancel()
	if started && !*clockOnly {
		<-finished
	}
	return scanner.Err()
}

func main() {
	if err := run(); err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"event": "error", "error": err.Error()})
		os.Exit(1)
	}
}
