package download

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveProgressArrivesBeforeChunkCommit(t *testing.T) {
	for _, ranged := range []bool{true, false} {
		t.Run(fmt.Sprintf("range=%t", ranged), func(t *testing.T) {
			payload := bytes.Repeat([]byte("data"), 16384)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if ranged {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(payload)-1, len(payload)))
					w.WriteHeader(206)
				}
				w.Write(payload[:4096])
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Write(payload[4096:])
			}))
			defer func() { unblock(); srv.Close() }()
			root, _ := os.OpenRoot(t.TempDir())
			defer root.Close()
			var committed atomic.Int64
			observed := make(chan Progress, 16)
			e := Engine{ChunkSize: int64(len(payload)), Observe: func(p Progress) {
				select {
				case observed <- p:
				default:
				}
			}}
			done := make(chan error, 1)
			go func() {
				done <- e.Fetch(context.Background(), root, "file.bin", "id", int64(len(payload)), "", func(context.Context) (Link, error) { return Link{URL: srv.URL}, nil }, func(n int64) { committed.Store(n) })
			}()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			found := false
			for !found {
				select {
				case p := <-observed:
					if p.Downloaded > 0 && p.Downloaded < int64(len(payload)) {
						if p.Received == 0 || p.Connections != 1 || committed.Load() != 0 {
							t.Fatalf("bad live progress: %+v committed=%d", p, committed.Load())
						}
						found = true
					}
				case <-deadline.C:
					t.Fatal("no partial progress before chunk completion")
				}
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			actual, err := root.ReadFile("file.bin")
			if err != nil || !bytes.Equal(actual, payload) {
				t.Fatal("file changed", err)
			}
			if committed.Load() != int64(len(payload)) {
				t.Fatal(committed.Load())
			}
		})
	}
}

func TestTrackerResumeDoesNotCountSavedBytesAsNetworkTraffic(t *testing.T) {
	var latest Progress
	tk := newTransferTracker(1000, 500, func(p Progress) { latest = p })
	if latest.Downloaded != 500 || latest.Received != 0 {
		t.Fatal(latest)
	}
	tk.begin()
	tk.read(0, 100)
	if latest.Downloaded != 600 || latest.Received != 100 {
		t.Fatal(latest)
	}
	tk.commit(0, 100)
	tk.end()
	if latest.Downloaded != 600 || latest.Received != 100 || latest.Connections != 0 {
		t.Fatal(latest)
	}
	tk.phase("verifying")
	if latest.Phase != "verifying" {
		t.Fatal(latest)
	}
}
