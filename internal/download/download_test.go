package download

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishedReceiptRecoversCrashBeforeTaskCheckpoint(t *testing.T) {
	payload := []byte("recoverable")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer s.Close()
	root, _ := os.OpenRoot(t.TempDir())
	defer root.Close()
	e := Engine{ChunkSize: 4}
	link := func(context.Context) (Link, error) { return Link{URL: s.URL}, nil }
	if err := e.Fetch(context.Background(), root, "file", "identity", int64(len(payload)), "", link, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat("file.pz.receipt"); err != nil {
		t.Fatal("missing publication receipt")
	}
	if err := e.Fetch(context.Background(), root, "file", "identity", int64(len(payload)), "", link, nil); err != nil {
		t.Fatal("cannot recover published file", err)
	}
	if err := e.Fetch(context.Background(), root, "file", "different", int64(len(payload)), "", link, nil); err == nil {
		t.Fatal("accepted foreign identity")
	}
}

func TestRangeResumeAndNoOverwrite(t *testing.T) {
	payload := bytes.Repeat([]byte("nas-data-"), 300)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var start, end int
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(206)
		w.Write(payload[start : end+1])
	}))
	defer server.Close()
	dir := t.TempDir()
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	e := Engine{ChunkSize: 512, Concurrency: 1}
	ctx, cancel := context.WithCancel(context.Background())
	link := func(context.Context) (Link, error) { return Link{URL: server.URL}, nil }
	err := e.Fetch(ctx, root, "movie.bin", "fid1", int64(len(payload)), "", link, func(n int64) {
		if n >= 512 {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("cancellation should interrupt")
	}
	before := calls.Load()
	hash := md5.Sum(payload)
	err = e.Fetch(context.Background(), root, "movie.bin", "fid1", int64(len(payload)), hex.EncodeToString(hash[:]), link, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := root.ReadFile("movie.bin")
	if !bytes.Equal(got, payload) {
		t.Fatal("corrupt download")
	}
	if calls.Load()-before >= int64((len(payload)+511)/512) {
		t.Fatal("did not resume")
	}
	root.Remove("movie.bin.pz.receipt")
	if err = e.Fetch(context.Background(), root, "movie.bin", "fid1", int64(len(payload)), "", link, nil); err == nil {
		t.Fatal("must not overwrite")
	}
}

func TestConcurrentRangeFailureCheckpointsCompletedParts(t *testing.T) {
	payload := []byte("abcdefghijklmnop")
	completed := make(chan struct{})
	var completedOnce sync.Once
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Error("invalid range", r.Header.Get("Range"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if start == 8 && fail.Load() {
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Error("other ranges did not complete")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	e := Engine{ChunkSize: 4, Concurrency: 3}
	link := func(context.Context) (Link, error) { return Link{URL: server.URL}, nil }
	if err = e.Fetch(context.Background(), root, "file", "id", int64(len(payload)), "", link, func(n int64) {
		if n >= 8 {
			completedOnce.Do(func() { close(completed) })
		}
	}); err == nil {
		t.Fatal("expected one range request to fail")
	}
	data, err := root.ReadFile("file.pz.json")
	if err != nil {
		t.Fatal("missing checkpoint after partial failure", err)
	}
	var saved journal
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Parts) < 2 {
		t.Fatalf("expected successful ranges to be checkpointed, got %v", saved.Parts)
	}
	fail.Store(false)
	if err = e.Fetch(context.Background(), root, "file", "id", int64(len(payload)), "", link, nil); err != nil {
		t.Fatal("resume failed", err)
	}
	got, err := root.ReadFile("file")
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published file mismatch: %q, %v", got, err)
	}
}

func TestResumeReDownloadsRecordedPartWithMissingDiskData(t *testing.T) {
	payload := []byte("abcdefgh")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-3" {
			t.Error("should only fetch the damaged part", r.Header.Get("Range"))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", "bytes 0-3/8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[:4])
	}))
	defer server.Close()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = root.WriteFile("file.pz.part", []byte("\x00\x00\x00\x00efgh"), 0600); err != nil {
		t.Fatal(err)
	}
	parts := map[int]string{}
	for i := 0; i < 2; i++ {
		h := sha256.Sum256(payload[i*4 : (i+1)*4])
		parts[i] = hex.EncodeToString(h[:])
	}
	if err = saveJournal(root, "file.pz.json", journal{Identity: "id", Size: 8, Chunk: 4, Parts: parts}); err != nil {
		t.Fatal(err)
	}
	err = (&Engine{Concurrency: 3}).Fetch(context.Background(), root, "file", "id", 8, "", func(context.Context) (Link, error) {
		return Link{URL: server.URL}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := root.ReadFile("file")
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published file mismatch: %q, %v", got, err)
	}
}

func TestParallelRangesStartBeforeFirstBodyCompletes(t *testing.T) {
	payload := []byte("abcdefghijkl")
	parallelStarted := make(chan struct{})
	var startedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Error("invalid range")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		if start == 0 {
			select {
			case <-parallelStarted:
			case <-time.After(3 * time.Second):
				t.Error("parallel ranges waited for the first body to finish")
				return
			}
		} else {
			startedOnce.Do(func() { close(parallelStarted) })
		}
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = (&Engine{ChunkSize: 4, Concurrency: 3}).Fetch(context.Background(), root, "file", "id", int64(len(payload)), "", func(context.Context) (Link, error) {
		return Link{URL: server.URL}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := root.ReadFile("file")
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published file mismatch: %q, %v", got, err)
	}
}

func TestRangeFailureCancelsFirstResponseStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-3" {
			w.Header().Set("Content-Range", "bytes 0-3/8")
			w.WriteHeader(http.StatusPartialContent)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = (&Engine{ChunkSize: 4, Concurrency: 2}).Fetch(ctx, root, "file", "id", 8, "", func(context.Context) (Link, error) {
		return Link{URL: server.URL}, nil
	}, nil)
	if err == nil {
		t.Fatal("accepted failed range")
	}
	if ctx.Err() != nil {
		t.Fatal("first response was not cancelled when the other range failed")
	}
}

func TestServerIgnoresRange(t *testing.T) {
	data := []byte("full response")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer s.Close()
	root, _ := os.OpenRoot(t.TempDir())
	defer root.Close()
	if err := (&Engine{ChunkSize: 4, Concurrency: 3}).Fetch(context.Background(), root, "a", "id", int64(len(data)), "", func(context.Context) (Link, error) { return Link{URL: s.URL}, nil }, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := root.ReadFile("a")
	if !bytes.Equal(got, data) {
		t.Fatal(string(got))
	}
}

func TestTransientRangesRetryWithoutCorruptingPublishedFile(t *testing.T) {
	for _, kind := range []string{"short", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			payload := []byte("abcdefghijkl")
			var mu sync.Mutex
			calls := map[int]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var start, end int
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				calls[start]++
				first := calls[start] == 1
				mu.Unlock()
				if first && kind == "unavailable" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
				w.WriteHeader(http.StatusPartialContent)
				if first {
					_, _ = w.Write(payload[start : start+1])
					return
				}
				_, _ = w.Write(payload[start : end+1])
			}))
			defer server.Close()
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			err = (&Engine{ChunkSize: 4, Concurrency: 3}).Fetch(context.Background(), root, "file", "id", int64(len(payload)), "", func(context.Context) (Link, error) {
				return Link{URL: server.URL}, nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := root.ReadFile("file")
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("corrupt retried file: %q, %v", got, err)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, start := range []int{0, 4, 8} {
				if calls[start] != 2 {
					t.Fatalf("range %d fetched %d times", start, calls[start])
				}
			}
		})
	}
}

func TestForbiddenRangesAreNotRetried(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = (&Engine{ChunkSize: 4, Concurrency: 3}).Fetch(context.Background(), root, "file", "id", 8, "", func(context.Context) (Link, error) { return Link{URL: server.URL}, nil }, nil)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("authorization failure retried: calls=%d error=%v", calls.Load(), err)
	}
}

func TestCancelInterruptsRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := retryTransfer(ctx, func() error {
		calls++
		cancel()
		return &retryableTransferError{errors.New("transient")}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel ignored: %v calls=%d", err, calls)
	}
}
func TestRejectShortRangeAndEscape(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-3/8")
		w.WriteHeader(206)
		w.Write([]byte("x"))
	}))
	defer s.Close()
	root, _ := os.OpenRoot(t.TempDir())
	defer root.Close()
	e := Engine{ChunkSize: 4}
	link := func(context.Context) (Link, error) { return Link{URL: s.URL}, nil }
	if err := e.Fetch(context.Background(), root, "a", "id", 8, "", link, nil); err == nil {
		t.Fatal("accepted truncated chunk")
	}
	if _, err := root.Stat("a"); !os.IsNotExist(err) {
		t.Fatal("published incomplete file")
	}
	for _, p := range []string{"../escape", "/absolute", "x\\..\\escape", "a:b"} {
		if err := ValidatePath(p); err == nil {
			t.Fatal("accepted path", p)
		}
	}
	if err := ValidatePath("课程/第一课.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := e.Fetch(context.Background(), root, "b", "id", 8, strings.Repeat("0", 32), link, nil); err == nil {
		t.Fatal("accepted bad file")
	}
}

func TestRejectNamesThatCollideWithRecoveryFiles(t *testing.T) {
	for _, p := range []string{"movie.pz.part", "movie.pz.json", "movie.pz.json.tmp", "movie.PZ.RECEIPT", "dir.pz.part/file"} {
		if err := ValidatePath(p); err == nil {
			t.Fatal("allowed recovery metadata collision", p)
		}
	}
}
