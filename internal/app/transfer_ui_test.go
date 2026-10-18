package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"panzhan/internal/quark"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in UI fixture: a real, throttled localhost download through the production
// task pipeline. It never contacts a cloud account or modifies application data.
func TestTransferUIFixture(t *testing.T) {
	if os.Getenv("PANZHAN_TRANSFER_UI_QA") != "1" {
		t.Skip("UI fixture is opt-in")
	}
	const size = 256 << 20
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file/download" {
			json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": []any{map[string]any{"download_url": upstream.URL + "/payload"}}})
			return
		}
		if r.URL.Path != "/payload" {
			w.WriteHeader(404)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.WriteHeader(206)
		block := make([]byte, 64<<10)
		for remaining := end - start + 1; remaining > 0; {
			n := min(int64(len(block)), remaining)
			if _, err := w.Write(block[:n]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			remaining -= n
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}))
	defer upstream.Close()
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.newProvider = func(c string, cb func(string)) *quark.Client { return quark.NewAt(upstream.URL, c, cb) }
	cookie, _ := a.seal("offline-fixture")
	if err := a.change(func(st *State) error {
		st.Salt = "offline-fixture"
		st.PasswordHash = passwordHash("offline-transfer-qa-2026", st.Salt)
		st.CookieCipher = cookie
		st.Settings.Concurrency = 3
		st.Settings.ChunkMiB = 1
		st.Jobs = []Job{{ID: "speed-fixture", Title: "实时下载测试（本地测试源）", Directory: "speed-test", Status: "queued", CloudDir: "existing", Batches: []Batch{{Done: true}}, Enumerated: true, Files: []SavedFile{{ID: "file", Path: "示例视频.mp4", Size: size}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:19869")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(a.Handler())
	server.Listener = listener
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); a.Run(ctx) }()
	// Working directory for this package is internal/app.
	stop := filepath.Join("..", "..", ".cache", "transfer-ui-qa.stop")
	_ = os.Remove(stop)
	t.Log("UI fixture ready at http://127.0.0.1:19869/")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(180 * time.Second)
	defer deadline.Stop()
	observed := false
loop:
	for {
		select {
		case <-deadline.C:
			break loop
		case <-ticker.C:
			jobs := a.withTransferProgress(a.snapshot().Jobs)
			if len(jobs) > 0 && jobs[0].Progress != nil && jobs[0].Progress.SpeedBPS > 0 {
				observed = true
			}
			if _, err := os.Stat(stop); err == nil {
				break loop
			}
		}
	}
	cancel()
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture worker did not stop")
	}
	if !observed {
		t.Fatal("no actual network speed was observed")
	}
}
