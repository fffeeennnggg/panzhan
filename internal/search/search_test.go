package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPanSearchExtractsPerLineTitlesAndCodes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("keyword") != "Blender 教程" {
			t.Error(r.URL)
		}
		data := map[string]any{"props": map[string]any{"pageProps": map[string]any{"data": map[string]any{"total": 1, "data": []any{map[string]any{"pan": "aliyundrive", "content": "1、<b>Blender 入门</b> <a href=\"https://pan.quark.cn/s/aaa\">https://pan.quark.cn/s/aaa</a> 提取码：1111\n2、Python 课程 https://pan.quark.cn/s/bbb 提取码：2222\n3、其他盘 https://example.com"}}}}}}
		b, _ := json.Marshal(data)
		w.Write([]byte(`<html><script id="__NEXT_DATA__" type="application/json">` + string(b) + `</script></html>`))
	}))
	defer srv.Close()
	r := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL, SearchPath: "/search?keyword=%s"}}).Search(context.Background(), "Blender 教程", []string{"pansearch"})
	if len(r.Results) != 2 || len(r.Sources) != 1 || r.Sources[0].Error != "" {
		t.Fatalf("%+v", r)
	}
	if r.Results[0].Title != "1、Blender 入门" || r.Results[0].URL != "https://pan.quark.cn/s/aaa?pwd=1111" || r.Results[1].Title != "2、Python 课程" || r.Results[1].URL != "https://pan.quark.cn/s/bbb?pwd=2222" {
		t.Fatalf("%+v", r.Results)
	}
}

func TestPanSearchDistinguishesEmptyResultsAndChangedPage(t *testing.T) {
	for _, tc := range []struct {
		page      string
		wantError bool
	}{
		{`<script id="__NEXT_DATA__">{"props":{"pageProps":{"data":{"total":0,"data":[]}}}}</script>`, false},
		{`<script id="__NEXT_DATA__">{"props":{"pageProps":{"other":[]}}}</script>`, true},
		{`<html>Check your browser</html>`, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tc.page)) }))
		r := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL, SearchPath: "/search?keyword=%s"}}).Search(context.Background(), "x", []string{"pansearch"})
		srv.Close()
		if len(r.Results) != 0 || (r.Sources[0].Error != "") != tc.wantError {
			t.Fatalf("%+v", r)
		}
	}
}

func TestPanSearchEncodedLinkDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<script id="__NEXT_DATA__">{"props":{"pageProps":{"data":{"data":[{"content":"T https&amp;#58;//pan.quark.cn/s/aaa"}]}}}}</script>`))
	}))
	defer srv.Close()
	r := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}}).Search(context.Background(), "x", []string{"pansearch"})
	if len(r.Results) != 1 || r.Results[0].Title != "T" {
		t.Fatalf("%+v", r)
	}
}

func TestPanSearchMultilineTitleAndCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<script id="__NEXT_DATA__">{"props":{"pageProps":{"data":{"data":[{"content":"Blender 教程<br>链接：https://pan.quark.cn/s/aaa<br>提取码：1234"}]}}}}</script>`))
	}))
	defer srv.Close()
	r := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}}).Search(context.Background(), "x", []string{"pansearch"})
	if len(r.Results) != 1 || r.Results[0].Title != "Blender 教程" || r.Results[0].URL != "https://pan.quark.cn/s/aaa?pwd=1234" {
		t.Fatalf("%+v", r)
	}
}

func TestPanSearchPayloadAfterMalformedTruncatedHighlight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><span class='highlight-...</div><script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"data":{"data":[{"content":"Blender https://pan.quark.cn/s/aaa"}]}}}}</script></body></html>`))
	}))
	defer srv.Close()
	r := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}}).Search(context.Background(), "x", []string{"pansearch"})
	if len(r.Results) != 1 || r.Results[0].Title != "Blender" {
		t.Fatalf("%+v", r)
	}
}

func TestPanSearchUsesNextDataAPIAndFetchesSecondPage(t *testing.T) {
	apiRequests := make(chan string, panSearchMaxPages)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			w.Write([]byte(`<script id="__NEXT_DATA__">{"buildId":"build-123"}</script>`))
			return
		}
		if r.URL.Path != "/_next/data/build-123/search.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Referer"), srv.URL+"/search?") {
			t.Errorf("missing search referer: %q", r.Header.Get("Referer"))
		}
		apiRequests <- r.URL.Query().Get("offset")
		offset := r.URL.Query().Get("offset")
		content := `Blender 入门 https://pan.quark.cn/s/aaa`
		if offset == "10" {
			content = `Blender 进阶 https://pan.quark.cn/s/bbb`
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pageProps":{"data":{"total":11,"data":[{"id":1,"pan":"quark","content":"` + content + `"}]}}}`))
	}))
	defer srv.Close()

	e := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}})
	report := e.Search(context.Background(), "Blender 教程", []string{"pansearch"})
	if len(report.Results) != 2 || report.Sources[0].Error != "" {
		t.Fatalf("report=%+v", report)
	}
	close(apiRequests)
	gotOffsets := map[string]bool{}
	for offset := range apiRequests {
		gotOffsets[offset] = true
	}
	if len(gotOffsets) != 2 || !gotOffsets["0"] || !gotOffsets["10"] {
		t.Fatalf("API offsets=%v", gotOffsets)
	}
}

func TestPanSearchRefreshesBuildIDWhenAPIIsStale(t *testing.T) {
	var pageRequests, staleRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			pageRequests++
			buildID := "old"
			if pageRequests > 1 {
				buildID = "new"
			}
			w.Write([]byte(`<script id="__NEXT_DATA__">{"buildId":"` + buildID + `"}</script>`))
			return
		}
		if r.URL.Path == "/_next/data/old/search.json" {
			staleRequests++
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"pageProps":{"data":{"total":1,"data":[{"content":"Blender https://pan.quark.cn/s/aaa"}]}}}`))
	}))
	defer srv.Close()

	report := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}}).Search(context.Background(), "Blender", []string{"pansearch"})
	if len(report.Results) != 1 || report.Sources[0].Error != "" || pageRequests != 2 || staleRequests != 1 {
		t.Fatalf("report=%+v pageRequests=%d staleRequests=%d", report, pageRequests, staleRequests)
	}
}

func TestSearchRetriesTransientFailuresAndCachesSuccess(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`<script id="__NEXT_DATA__">{"props":{"pageProps":{"data":{"total":0,"data":[]}}}}</script>`))
	}))
	defer srv.Close()
	e := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}})
	first := e.Search(context.Background(), "retry test", []string{"pansearch"})
	second := e.Search(context.Background(), "retry test", []string{"pansearch"})
	if requests.Load() != 2 || len(first.Sources) != 1 || first.Sources[0].Error != "" || len(second.Sources) != 1 || second.Sources[0].Error != "" {
		t.Fatalf("requests=%d first=%+v second=%+v", requests.Load(), first, second)
	}
}

func TestPanSearchKeepsFirstPageWhenLaterPageFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			w.Write([]byte(`<script id="__NEXT_DATA__">{"buildId":"build-123"}</script>`))
			return
		}
		if r.URL.Query().Get("offset") == "10" {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"pageProps":{"data":{"total":11,"data":[{"content":"Blender https://pan.quark.cn/s/aaa"}]}}}`))
	}))
	defer srv.Close()

	report := NewWithSources([]Source{{ID: "pansearch", Name: "PanSearch", Base: srv.URL}}).Search(context.Background(), "Blender", []string{"pansearch"})
	if len(report.Results) != 1 || report.Sources[0].Error == "" {
		t.Fatalf("report=%+v", report)
	}
}

func TestPanHubSourceCatalogExcludesDeadSitesAndAddsAdapters(t *testing.T) {
	sources := New().Sources()
	ids := map[string]bool{}
	for _, source := range sources {
		ids[source.ID] = true
	}
	for _, id := range []string{"pansearch", "qupansou", "panta", "hunhepan", "jikepan", "duoduo", "xuexizhinan", "telegram"} {
		if !ids[id] {
			t.Errorf("missing source %q", id)
		}
	}
	for _, id := range []string{"labi", "zhizhen"} {
		if ids[id] {
			t.Errorf("dead source %q remains in catalog", id)
		}
	}
}

func TestQupansouSearchPostsAndKeepsQuarkLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var body struct {
			Query struct {
				SearchText string `json:"searchtext"`
			} `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Query.SearchText != "Blender 教程" {
			t.Errorf("bad request body: %+v err=%v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":200,"data":[{"id":7,"title":"Blender 教程","url":"https://pan.quark.cn/s/abcd?pwd=1234"},{"id":8,"title":"另一个网盘","url":"https://pan.baidu.com/s/x"}]}`))
	}))
	defer srv.Close()

	report := NewWithSources([]Source{{ID: "qupansou", Name: "夸克搜", Base: srv.URL}}).Search(context.Background(), "Blender 教程", []string{"qupansou"})
	if len(report.Results) != 1 || report.Results[0].Title != "Blender 教程" || report.Results[0].URL != "https://pan.quark.cn/s/abcd?pwd=1234" || report.Sources[0].Error != "" {
		t.Fatalf("%+v", report)
	}
}

func TestJikepanSearchPostsAndFiltersQuarkLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "Blender" {
			t.Errorf("bad request %s %+v", r.Method, body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"msg":"success","list":[{"name":"Blender 课程","links":[{"service":"quark","link":"https://pan.quark.cn/s/abcd","pwd":"1234"},{"service":"baidu","link":"https://pan.baidu.com/s/xyz"}]}]}`))
	}))
	defer srv.Close()

	report := NewWithSources([]Source{{ID: "jikepan", Name: "极客盘", Base: srv.URL}}).Search(context.Background(), "Blender", []string{"jikepan"})
	if len(report.Results) != 1 || report.Results[0].Title != "Blender 课程" || report.Results[0].URL != "https://pan.quark.cn/s/abcd?pwd=1234" {
		t.Fatalf("%+v", report)
	}
}

func TestTelegramChannelSearchExtractsQuarkShareAndPassword(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/s/channel_one" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`<div class="tgme_widget_message_wrap"><div class="tgme_widget_message" data-post="channel_one/123"><div class="tgme_widget_message_text">Blender 完整教程<br><a href="https://pan.quark.cn/s/abcd">夸克链接</a><br>提取码：1234</div><time datetime="2026-01-01T00:00:00+00:00"></time></div></div>`))
	}))
	defer srv.Close()

	report := NewWithSources([]Source{{ID: "telegram", Name: "Telegram", Base: srv.URL + "/s", Channels: []string{"channel_one"}}}).Search(context.Background(), "Blender", []string{"telegram"})
	if len(report.Results) != 1 || report.Results[0].Title != "Blender 完整教程" || report.Results[0].URL != "https://pan.quark.cn/s/abcd?pwd=1234" {
		t.Fatalf("%+v", report)
	}
}

func TestSearchSourceRunsOnlyRequestedTelegramChannel(t *testing.T) {
	requested := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		w.Write([]byte(`<div class="tgme_widget_message_wrap"><div class="tgme_widget_message_text">Blender https://pan.quark.cn/s/abcd</div></div>`))
	}))
	defer srv.Close()
	e := NewWithSources([]Source{{ID: "telegram", Name: "Telegram", Base: srv.URL + "/s", Channels: []string{"priority", "normal"}, PriorityChannels: []string{"priority"}}})

	report := e.SearchSource(context.Background(), "Blender", "telegram", "normal")
	if len(report.Results) != 1 || len(requested) != 1 || requested[0] != "/s/normal" || report.Sources[0].ChannelCount != 1 {
		t.Fatalf("report=%+v requested=%v", report, requested)
	}
	bad := e.SearchSource(context.Background(), "Blender", "telegram", "unknown")
	if len(bad.Results) != 0 || !strings.Contains(bad.Sources[0].Error, "频道") {
		t.Fatalf("unexpected invalid-channel result: %+v", bad)
	}
}

func TestPantaSearchFetchesTopicThread(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("keyword") != "Blender" {
				t.Errorf("unexpected search query %s", r.URL.RawQuery)
			}
			w.Write([]byte(`<div class="topicItem"><a href="thread?topicId=42">Blender 完整教程</a></div>`))
		case "/thread":
			if r.URL.Query().Get("topicId") != "42" {
				t.Errorf("unexpected topic id %s", r.URL.RawQuery)
			}
			w.Write([]byte(`<div>链接：https://pan.quark.cn/s/abcd 提取码：1234</div>`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	report := NewWithSources([]Source{{ID: "panta", Name: "盘他", Base: srv.URL}}).Search(context.Background(), "Blender", []string{"panta"})
	if len(report.Results) != 1 || report.Results[0].Title != "Blender 完整教程" || report.Results[0].URL != "https://pan.quark.cn/s/abcd?pwd=1234" || report.Sources[0].Error != "" {
		t.Fatalf("%+v", report)
	}
}

func TestSearchParsesDetailsAndKeepsPartialErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/vod/detail/id/1.html" {
			w.Write([]byte(`<a href="https://pan.quark.cn/s/abcd?pwd=1234">夸克</a>`))
			return
		}
		w.Write([]byte(`<div class="module-search-item"><div class="video-info-header"><h3><a href="/vod/detail/id/1.html">Python &amp; 课程</a></h3></div></div>`))
	}))
	defer srv.Close()
	e := NewWithSources([]Source{{ID: "a", Name: "A", Base: srv.URL}, {ID: "b", Name: "B", Base: srv.URL, SearchPath: "/bad"}})
	report := e.Search(context.Background(), "Python", []string{"a", "b"})
	if len(report.Results) != 1 || report.Results[0].Title != "Python & 课程" || report.Results[0].URL != "https://pan.quark.cn/s/abcd?pwd=1234" {
		t.Fatalf("%+v", report)
	}
	if len(report.Sources) != 2 {
		t.Fatal(report)
	}
	found := false
	for _, s := range report.Sources {
		if s.ID == "b" && s.Error != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing source failure", report)
	}
}
func TestLinkDedupAndPassword(t *testing.T) {
	links := ExtractLinks(`<a href="https://pan.quark.cn/s/abc?pwd=1234">x</a> https://pan.quark.cn/s/abc?pwd=1234`)
	if len(links) != 1 || links[0] != "https://pan.quark.cn/s/abc?pwd=1234" {
		t.Fatal(links)
	}
}

func TestPasswordsStayWithTheirOwnShare(t *testing.T) {
	got := ExtractLinks(`https://pan.quark.cn/s/aaa 提取码：1111 https://pan.quark.cn/s/bbb 提取码：2222`)
	if len(got) != 2 || got[0] != "https://pan.quark.cn/s/aaa?pwd=1111" || got[1] != "https://pan.quark.cn/s/bbb?pwd=2222" {
		t.Fatal(got)
	}
}
