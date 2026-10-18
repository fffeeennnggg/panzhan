// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"math"
	"panzhan/internal/download"
	"time"
)

type TaskProgress struct {
	DownloadedBytes     int64   `json:"downloaded_bytes"`
	TotalBytes          int64   `json:"total_bytes"`
	SpeedBPS            float64 `json:"speed_bps"`
	ETASeconds          *int64  `json:"eta_seconds"`
	ElapsedSeconds      int64   `json:"elapsed_seconds"`
	CurrentFile         string  `json:"current_file"`
	FileDownloadedBytes int64   `json:"file_downloaded_bytes"`
	FileTotalBytes      int64   `json:"file_total_bytes"`
	Connections         int     `json:"connections"`
	Phase               string  `json:"phase"`
}
type transferSample struct {
	at       time.Time
	received int64
}
type transferState struct {
	started, lastTraffic               time.Time
	fileIndex                          int
	current                            TaskProgress
	network, receiveBase, fileReceived int64
	samples                            []transferSample
}

func (a *App) beginTransfer(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.transfers == nil {
		a.transfers = map[string]*transferState{}
	}
	now := time.Now()
	a.transfers[id] = &transferState{started: now, fileIndex: -1, samples: []transferSample{{at: now}}}
}
func (a *App) endTransfer(id string) { a.mu.Lock(); defer a.mu.Unlock(); delete(a.transfers, id) }
func (a *App) beginTransferFile(id string, index int, file SavedFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.transfers[id]
	if t == nil {
		return
	}
	t.fileIndex = index
	t.receiveBase = t.network
	t.fileReceived = 0
	t.current = TaskProgress{CurrentFile: file.Path, FileTotalBytes: file.Size, FileDownloadedBytes: file.Downloaded, Phase: "preparing"}
}
func (t *transferState) observe(p download.Progress, now time.Time) {
	if p.Received > t.fileReceived {
		t.lastTraffic = now
	}
	t.fileReceived = p.Received
	t.network = t.receiveBase + p.Received
	t.current.FileDownloadedBytes = p.Downloaded
	t.current.Connections = p.Connections
	t.current.Phase = p.Phase
	t.samples = append(t.samples, transferSample{now, t.network})
	for len(t.samples) > 2 && now.Sub(t.samples[1].at) > 6*time.Second {
		t.samples = t.samples[1:]
	}
}
func (a *App) observeTransfer(id string, p download.Progress) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := a.transfers[id]; t != nil {
		t.observe(p, time.Now())
	}
}
func (t *transferState) view(job Job, now time.Time) TaskProgress {
	p := t.current
	p.ElapsedSeconds = max(0, int64(now.Sub(t.started).Seconds()))
	for i, f := range job.Files {
		p.TotalBytes += f.Size
		n := f.Downloaded
		if f.Done {
			n = f.Size
		}
		if i == t.fileIndex {
			n = p.FileDownloadedBytes
		}
		p.DownloadedBytes += max(0, min(f.Size, n))
	}
	if job.Status != "downloading" || p.Phase != "downloading" || t.lastTraffic.IsZero() || now.Sub(t.lastTraffic) > 4*time.Second {
		return p
	}
	if len(t.samples) > 0 {
		baseline := t.samples[0]
		for _, s := range t.samples {
			if now.Sub(s.at) >= 6*time.Second {
				baseline = s
			} else {
				break
			}
		}
		elapsed := now.Sub(baseline.at).Seconds()
		elapsed = math.Min(elapsed, 6)
		if elapsed >= 0.25 {
			p.SpeedBPS = float64(max(0, t.network-baseline.received)) / elapsed
		}
	}
	if p.SpeedBPS > 0 && p.TotalBytes > p.DownloadedBytes {
		eta := int64(math.Ceil(float64(p.TotalBytes-p.DownloadedBytes) / p.SpeedBPS))
		p.ETASeconds = &eta
	}
	return p
}

// Rates are attached only to API copies. Recovery state still counts verified
// chunks, never uncommitted network buffers or transfer-rate samples.
func (a *App) withTransferProgress(jobs []Job) []Job {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for i := range jobs {
		jobs[i].Progress = nil
		if jobs[i].Status != "downloading" && jobs[i].Status != "verifying" && jobs[i].Status != "transferring" {
			continue
		}
		if t := a.transfers[jobs[i].ID]; t != nil {
			p := t.view(jobs[i], now)
			jobs[i].Progress = &p
		}
	}
	return jobs
}
