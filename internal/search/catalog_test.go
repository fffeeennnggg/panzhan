package search

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogRestartMergeAndSourceIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.json")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return first }
	a := Source{ID: "a", Name: "A"}
	b := Source{ID: "b", Name: "B"}
	_, err = c.Remember([]Result{{Title: "Blender 教程", URL: "https://pan.quark.cn/s/abc?pwd=1234"}}, a)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return first.Add(24 * time.Hour) }
	_, err = c.Remember([]Result{
		{Title: "Blender 教程", URL: "https://pan.quark.cn/s/abc"},
		{Title: "Blender 教程", URL: "https://pan.quark.cn/s/def"},
		{Title: "Blender 子目录", URL: "https://pan.quark.cn/s/abc#/list/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Title: "Blender 假链接", URL: "https://evil.test/"},
	}, b)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Search("blender 教程", []string{"a", "b"})
	if len(got) != 2 {
		t.Fatalf("same titles should keep distinct shares: %+v", got)
	}
	all := restored.Search("Blender", []string{"a", "b"})
	if len(all) != 3 {
		t.Fatalf("subfolder lost: %+v", all)
	}
	onlyA := restored.Search("Blender", []string{"a"})
	if len(onlyA) != 1 || onlyA[0].URL != "https://pan.quark.cn/s/abc?pwd=1234" || !onlyA[0].Local {
		t.Fatalf("%+v", onlyA)
	}
	if onlyA[0].DiscoveredAt != first.Format(time.RFC3339) || onlyA[0].LastSeenAt == onlyA[0].DiscoveredAt || onlyA[0].CheckedAt != "" {
		t.Fatalf("incorrect time semantics: %+v", onlyA)
	}
	if len(restored.Search("Blender", nil)) != 0 {
		t.Fatal("disabled sources leaked")
	}
	if len(all[0].Origins) == 0 {
		t.Fatal("missing origins")
	}
	for _, r := range all {
		if r.ID == "quark:abc:0" && len(r.Origins) != 2 {
			t.Fatalf("origins not merged: %+v", r)
		}
	}
}

func TestCatalogConcurrentWritesAndBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.json")
	c, _ := OpenCatalog(path)
	c.limit = 8
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.Remember([]Result{{Title: "课程", URL: fmt.Sprintf("https://pan.quark.cn/s/item%d", i)}}, Source{ID: "a", Name: "A"})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	again, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Search("课程", []string{"a"})) != 8 {
		t.Fatal("catalog cap or writes broken")
	}
}

func TestCatalogCorruptFilePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.json")
	os.WriteFile(path, []byte("broken"), 0600)
	if _, err := OpenCatalog(path); err == nil {
		t.Fatal("corruption silently ignored")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "broken" {
		t.Fatal("corrupt data overwritten")
	}
}

func TestSuccessfulSourceCachedDespiteOtherFailure(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(403)
			return
		}
		hits.Add(1)
		fmt.Fprint(w, `<script id="__NEXT_DATA__">{"props":{"pageProps":{"data":{"data":[{"content":"Blender https://pan.quark.cn/s/abcd"}]}}}}</script>`)
	}))
	defer srv.Close()
	e := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}, {ID: "bad", Name: "Bad", Base: srv.URL, SearchPath: "/bad"}})
	e.Search(context.Background(), "Blender", []string{"pansearch", "bad"})
	got := e.Search(context.Background(), "Blender", []string{"pansearch", "bad"})
	if hits.Load() != 1 || len(got.Results) != 1 {
		t.Fatalf("successful source fetched again: hits=%d report=%+v", hits.Load(), got)
	}
}

func TestSourceBackoffRecoveryAndCancellation(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(403) }))
	defer srv.Close()
	e := NewWithSources([]Source{{ID: "a", Name: "A", Base: srv.URL}})
	now := time.Now()
	e.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		e.SearchSource(context.Background(), "q", "a", "")
	}
	skipped := e.SearchSource(context.Background(), "q", "a", "")
	if hits.Load() != 3 || skipped.Sources[0].RetryAt == "" {
		t.Fatalf("backoff missing: hits=%d %+v", hits.Load(), skipped)
	}
	now = now.Add(2 * time.Minute)
	e.SearchSource(context.Background(), "q", "a", "")
	if hits.Load() != 4 {
		t.Fatal("no recovery probe")
	}
	e2 := NewWithSources([]Source{{ID: "a", Name: "A", Base: srv.URL}})
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		e2.SearchSource(ctx, "q", "a", "")
	}
	got := e2.SearchSource(context.Background(), "q", "a", "")
	if got.Sources[0].RetryAt != "" {
		t.Fatal("user cancellation counted as source failure")
	}
}

func TestPartialResultsStillCountAsSourceFailure(t *testing.T) {
	var hits atomic.Int32
	var searches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/search" {
			searches.Add(1)
		}
		if r.URL.Query().Get("offset") == "10" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/search.json") {
			fmt.Fprint(w, `{"pageProps":{"data":{"data":[{"content":"Blender https://pan.quark.cn/s/abcd"}],"total":20}}}`)
			return
		}
		fmt.Fprint(w, `<script id="__NEXT_DATA__">{"buildId":"test","props":{"pageProps":{"data":{"data":[{"content":"Blender https://pan.quark.cn/s/abcd"}],"total":20}}}}</script>`)
	}))
	defer srv.Close()
	e := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}})
	now := time.Now()
	e.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		report := e.SearchSource(context.Background(), "Blender", "pansearch", "")
		if len(report.Results) != 1 || report.Sources[0].Error == "" {
			t.Fatalf("partial results/error lost: %+v", report)
		}
	}
	e.SearchSource(context.Background(), "Blender", "pansearch", "")
	if searches.Load() != 3 {
		t.Fatalf("partial failures did not back off, searches=%d total requests=%d", searches.Load(), hits.Load())
	}
}
