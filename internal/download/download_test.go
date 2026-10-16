package download

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
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
