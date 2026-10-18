// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type downloadProbeOptions struct {
	FileIndex int `json:"file_index"`
	Workers   int `json:"workers"`
	PartMiB   int `json:"part_mib"`
	Seconds   int `json:"seconds"`
}
type downloadProbeResult struct {
	Received       int64          `json:"received_bytes"`
	Seconds        float64        `json:"elapsed_seconds"`
	Speed          float64        `json:"speed_bps"`
	CompleteRanges int64          `json:"complete_ranges"`
	Requests       int64          `json:"requests"`
	MeanHeaders    float64        `json:"mean_headers_seconds"`
	Protocols      map[string]int `json:"protocols"`
	Statuses       map[int]int    `json:"statuses"`
	Failures       int64          `json:"failures"`
	DiskBytes      int            `json:"disk_test_bytes"`
	DiskSeconds    float64        `json:"disk_write_sync_seconds"`
	DiskError      string         `json:"disk_error,omitempty"`
}
type probeReader struct {
	io.Reader
	received *atomic.Int64
}

func (r probeReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.received.Add(int64(n))
	return n, err
}

// This diagnostic only reads an existing task's cloud file, with bounded
// time/traffic. It never changes task files or exposes signed URLs or cookies.
func (a *App) diagnoseDownload(ctx context.Context, id string, in downloadProbeOptions) (downloadProbeResult, error) {
	var result downloadProbeResult
	if in.Workers == 0 {
		in.Workers = 3
	}
	if in.PartMiB == 0 {
		in.PartMiB = 10
	}
	if in.Seconds == 0 {
		in.Seconds = 10
	}
	if in.Workers < 1 || in.Workers > 8 || in.PartMiB < 1 || in.PartMiB > 10 || in.Seconds < 1 || in.Seconds > 15 {
		return result, errors.New("测速并发需为 1–8，分片 1–10 MiB，时长 1–15 秒")
	}
	job, err := a.job(id)
	if err != nil {
		return result, err
	}
	if in.FileIndex < 0 || in.FileIndex >= len(job.Files) || job.Files[in.FileIndex].Size <= 0 {
		return result, errors.New("请选择任务中已完成转存的非空文件")
	}
	a.mu.Lock()
	busy := a.diagnosing || len(a.active) > 0
	for _, j := range a.state.Jobs {
		if j.Status == "queued" || j.Status == "transferring" || j.Status == "downloading" || j.Status == "verifying" {
			busy = true
		}
	}
	if busy {
		a.mu.Unlock()
		return result, errors.New("请先暂停下载，待任务停止后再测速，避免争抢带宽")
	}
	a.diagnosing = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.diagnosing = false; a.mu.Unlock(); a.signal() }()
	// Bound link resolution, disk sync and streaming to the HTTP caller's lifetime.
	bounded, stop := context.WithTimeout(ctx, 40*time.Second)
	defer stop()
	c, err := a.provider()
	if err != nil {
		return result, err
	}
	file := job.Files[in.FileIndex]
	link, err := c.DownloadLink(bounded, file.ID)
	if err != nil {
		return result, err
	}
	rootPath := job.DownloadRoot
	if rootPath == "" {
		rootPath = a.rootPath
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		result.DiskError = "无法访问下载目录"
	} else {
		func() {
			defer root.Close()
			buf := make([]byte, 8<<20)
			if _, er := rand.Read(buf); er != nil {
				result.DiskError = "无法建立测速数据"
				return
			}
			name := ".panzhan-speed-" + randomID()
			f, er := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if er != nil {
				result.DiskError = "无法创建临时测速文件"
				return
			}
			defer func() {
				f.Close()
				if er := root.Remove(name); er != nil {
					result.DiskError = "临时测速文件清理失败"
				}
			}()
			started := time.Now()
			n, er := f.Write(buf)
			if er == nil {
				er = f.Sync()
			}
			result.DiskSeconds = time.Since(started).Seconds()
			result.DiskBytes = n
			if er != nil || n != len(buf) {
				result.DiskError = "测速文件写入或同步失败"
			}
		}()
	}
	sample, cancel := context.WithTimeout(bounded, time.Duration(in.Seconds)*time.Second)
	defer cancel()
	result.Protocols = map[string]int{}
	result.Statuses = map[int]int{}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var received, requests, completed, failures, next atomic.Int64
	var mu sync.Mutex
	var headerTime float64
	var headers int
	chunk := int64(in.PartMiB) << 20
	limit := min(file.Size, int64(64<<20))
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < in.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 256<<10)
			for sample.Err() == nil {
				from := (next.Add(1) - 1) * chunk
				if from >= limit {
					return
				}
				to := min(limit-1, from+chunk-1)
				req, er := http.NewRequestWithContext(sample, "GET", link.URL, nil)
				if er != nil {
					failures.Add(1)
					return
				}
				req.Header = link.Header.Clone()
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
				req.Header.Set("Accept-Encoding", "identity")
				t := time.Now()
				requests.Add(1)
				res, er := client.Do(req)
				if er != nil {
					if sample.Err() == nil {
						failures.Add(1)
					}
					return
				}
				mu.Lock()
				headerTime += time.Since(t).Seconds()
				headers++
				result.Protocols[res.Proto]++
				result.Statuses[res.StatusCode]++
				mu.Unlock()
				if res.StatusCode != http.StatusPartialContent || res.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", from, to, file.Size) {
					res.Body.Close()
					failures.Add(1)
					return
				}
				n, er := io.CopyBuffer(io.Discard, io.LimitReader(probeReader{res.Body, &received}, to-from+2), buf)
				res.Body.Close()
				if er != nil || n != to-from+1 {
					if sample.Err() == nil {
						failures.Add(1)
					}
					return
				}
				completed.Add(1)
			}
		}()
	}
	wg.Wait()
	result.Seconds = max(time.Since(start).Seconds(), 1e-9)
	result.Received = received.Load()
	result.Speed = float64(result.Received) / result.Seconds
	result.Requests = requests.Load()
	result.CompleteRanges = completed.Load()
	result.Failures = failures.Load()
	result.MeanHeaders = headerTime / float64(max(1, headers))
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}
