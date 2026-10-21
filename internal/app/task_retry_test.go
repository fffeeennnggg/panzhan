package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"panzhan/internal/quark"
)

func TestWorkerAutomaticallyRecoversTransientDownloadFailure(t *testing.T) {
	const payload = "resumed automatically"
	var linkCalls, saveCalls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file/download":
			if linkCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": []any{map[string]any{"download_url": srv.URL + "/blob"}}})
		case "/blob":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte(payload))
		default:
			saveCalls.Add(1)
			t.Errorf("repeated cloud operation: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.newProvider = func(c string, cb func(string)) *quark.Client { return quark.NewAt(srv.URL, c, cb) }
	cipher, err := a.seal("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.change(func(st *State) error {
		st.CookieCipher = cipher
		st.Jobs = []Job{{ID: "retry-worker", Directory: "retry-worker", Status: "queued", CloudDir: "saved", Enumerated: true, Batches: []Batch{{Done: true}}, Files: []SavedFile{{ID: "owned", Path: "file.txt", Size: int64(len(payload))}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.Run(ctx) }()
	defer func() { cancel(); <-done }()
	sawRetry := false
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		j, err := a.job("retry-worker")
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == "retrying" {
			sawRetry = true
			if linkCalls.Load() != 1 || j.NextRetryAt == "" {
				t.Fatalf("busy loop or missing retry deadline: %+v", j)
			}
		}
		if j.Status == "attention" {
			t.Fatalf("temporary failure still needs manual action: %+v", j)
		}
		if j.Status == "completed" {
			data, err := os.ReadFile(filepath.Join(a.rootPath, j.Directory, "file.txt"))
			if err != nil || string(data) != payload || !sawRetry || linkCalls.Load() != 2 || saveCalls.Load() != 0 {
				t.Fatalf("invalid automatic recovery: data=%q err=%v sawRetry=%v links=%d saves=%d", data, err, sawRetry, linkCalls.Load(), saveCalls.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("worker never resumed without user action")
}

func TestRetryDeadlineAndPauseCancel(t *testing.T) {
	for _, action := range []string{"pause", "cancel"} {
		t.Run(action, func(t *testing.T) {
			a, err := New(t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			due := time.Now().Add(time.Minute).Truncate(time.Second)
			if err := a.change(func(st *State) error {
				st.Jobs = []Job{{ID: "waiting", Status: "retrying", RetryCount: 3, NextRetryAt: due.Format(time.RFC3339)}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			a.queueDueRetries(due.Add(-time.Second))
			j, _ := a.job("waiting")
			if j.Status != "retrying" {
				t.Fatal("task was queued before its retry deadline")
			}
			if err := a.action(context.Background(), j.ID, action, ""); err != nil {
				t.Fatal(err)
			}
			a.queueDueRetries(due.Add(time.Second))
			j, _ = a.job(j.ID)
			if (j.Status != "paused" && j.Status != "cancelled") || j.NextRetryAt != "" {
				t.Fatalf("stopped retry was reactivated: %+v", j)
			}
		})
	}
}

func TestTaskRetryDelayCapsAtFifteenMinutes(t *testing.T) {
	for count, want := range map[int]time.Duration{1: 15 * time.Second, 2: 30 * time.Second, 3: time.Minute, 20: 15 * time.Minute} {
		if got := taskRetryDelay(count); got != want {
			t.Fatalf("count %d: got %s want %s", count, got, want)
		}
	}
}

func TestRetryPersistenceFailureDoesNotReportScheduled(t *testing.T) {
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.state.Jobs = []Job{{ID: "unsaved", Status: "downloading"}}
	// A file at the state directory makes the next checkpoint deterministically fail.
	badDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(badDir, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a.dataDir = badDir
	if a.recordRunFailure("unsaved", &quark.TemporaryError{Err: errors.New("offline")}) {
		t.Fatal("retry reported scheduled despite failed persistence")
	}
}

func TestUpgradeSchedulesOnlyLegacyTransientFailures(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	a, err := New(dataDir, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.change(func(st *State) error {
		st.Jobs = []Job{
			{ID: "network", Status: "attention", Error: "下载网络异常，分片重试后仍未恢复，可重试以重新获取直链"},
			{ID: "busy", Status: "attention", Error: "夸克 HTTP 503，请检查账号或网络"},
			{ID: "known-task", Status: "attention", Error: "转存仍未结束，请稍后重试以查询同一个任务", Batches: []Batch{{TaskID: "keep-same-task"}}},
			{ID: "auth", Status: "attention", Error: "夸克 HTTP 401，请检查账号或网络"},
			{ID: "disk", Status: "attention", Error: "NAS 目标目录不可写，请检查目录授权"},
			{ID: "uncertain", Status: "attention", Uncertain: true, Error: "夸克 HTTP 503，请检查账号或网络"},
			{ID: "paused", Status: "paused", Error: "夸克 HTTP 503，请检查账号或网络"},
			{ID: "cancelled", Status: "cancelled", Error: "夸克 HTTP 503，请检查账号或网络"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err = New(dataDir, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"network", "busy", "known-task"} {
		j, _ := a.job(id)
		if j.Status != "retrying" || j.NextRetryAt == "" {
			t.Fatalf("legacy transient task still requires a click: %+v", j)
		}
	}
	for _, id := range []string{"auth", "disk", "uncertain", "paused", "cancelled"} {
		j, _ := a.job(id)
		if j.Status == "retrying" || j.NextRetryAt != "" {
			t.Fatalf("non-transient or stopped task reactivated: %+v", j)
		}
	}
}
