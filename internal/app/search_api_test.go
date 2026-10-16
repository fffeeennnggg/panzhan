package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"panzhan/internal/search"
)

func TestLocalSearchAPIAuthPersistenceAndDisabledSources(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	// Populate via the real source engine, then reopen the application as after restart.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<script id="__NEXT_DATA__">{"props":{"pageProps":{"data":{"data":[{"content":"Blender 教程 https://pan.quark.cn/s/abc"}]}}}}</script>`))
	}))
	defer upstream.Close()
	// Engine catalog persistence is also tested at the package boundary.
	catalog, err := search.OpenCatalog(filepath.Join(dir, "resources.json"))
	if err != nil {
		t.Fatal(err)
	}
	engine := search.NewWithSources([]search.Source{{ID: "pansearch", Name: "PanSearch", Base: upstream.URL}})
	report := engine.Search(context.Background(), "Blender", []string{"pansearch"})
	if len(report.Results) != 1 {
		t.Fatal(report)
	}
	if _, err = catalog.Remember(report.Results, search.Source{ID: "pansearch", Name: "PanSearch"}); err != nil {
		t.Fatal(err)
	}
	a, err = New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	a.sessions["local-test"] = session{expires: time.Now().Add(time.Hour)}
	request := func(query string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://localhost/api/search/local", strings.NewReader(query))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		if auth {
			req.AddCookie(&http.Cookie{Name: "panzhan_session", Value: "local-test"})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		return w
	}
	if w := request(`{"query":"Blender"}`, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(`{"query":"Blender"}`, true)
	var got search.Report
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Results) != 1 || !got.Results[0].Local {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	a.state.Settings.Sources = []string{}
	w = request(`{"query":"Blender"}`, true)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Results) != 0 {
		t.Fatal(w.Body.String())
	}
	if w := request(`{"query":" "}`, true); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestSearchAPIRequestsOnlySelectedTelegramChannel(t *testing.T) {
	requested := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- r.URL.Path
		w.Write([]byte(`<div class="tgme_widget_message_wrap"><div class="tgme_widget_message_text">Blender https://pan.quark.cn/s/abcd</div></div>`))
	}))
	defer upstream.Close()

	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.search = search.NewWithSources([]search.Source{{ID: "telegram", Name: "Telegram", Base: upstream.URL + "/s", Channels: []string{"priority", "normal"}}})
	a.state.Settings.Sources = []string{"telegram"}
	a.sessions["search-api-test"] = session{expires: time.Now().Add(time.Hour)}
	server := httptest.NewServer(a.Handler())
	defer server.Close()

	requestBody, _ := json.Marshal(map[string]string{"query": "Blender", "source_id": "telegram", "channel": "normal"})
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/search", strings.NewReader(string(requestBody)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", server.URL)
	req.AddCookie(&http.Cookie{Name: "panzhan_session", Value: "search-api-test"})
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var report search.Report
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].URL != "https://pan.quark.cn/s/abcd" {
		t.Fatalf("report=%+v", report)
	}
	if got := <-requested; got != "/s/normal" {
		t.Fatalf("requested %s", got)
	}
	select {
	case extra := <-requested:
		t.Fatalf("unexpected extra source request %s", extra)
	default:
	}
}
