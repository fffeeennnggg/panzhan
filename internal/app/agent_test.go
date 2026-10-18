package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"panzhan/internal/quark"
	"panzhan/internal/search"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func agentRequest(a *App, method, path, token string, payload any, admin bool) *httptest.ResponseRecorder {
	var data []byte
	if payload != nil {
		data, _ = json.Marshal(payload)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if payload != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if admin {
		r.AddCookie(&http.Cookie{Name: "panzhan_session", Value: "admin-fixture"})
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func grantForTest(t *testing.T, a *App, name string, scopes []string) (AgentGrant, string) {
	t.Helper()
	g, token, err := a.createAgent(name, "", scopes, 30)
	if err != nil {
		t.Fatal(err)
	}
	return g, token
}
func agentFixture(t *testing.T) (*App, string, string, *atomic.Int64) {
	t.Helper()
	payload := []byte("verified agent download bytes")
	var saved atomic.Int64
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(map[string]any{"status": 200, "code": 0, "data": v}) }
		switch r.URL.Path {
		case "/share/sharepage/token":
			write(map[string]string{"stoken": "private-share-token"})
		case "/share/sharepage/detail":
			write(map[string]any{"list": []any{map[string]any{"fid": "shared", "file_name": "demo.bin", "file": true, "size": len(payload), "share_fid_token": "private-file-token"}}})
		case "/file/sort":
			files := []any{}
			if r.URL.Query().Get("pdir_fid") == "batch-folder" {
				files = append(files, map[string]any{"fid": "owned", "file_name": "demo.bin", "file": true, "size": len(payload)})
			}
			write(map[string]any{"list": files})
		case "/file":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			id := "cloud"
			if strings.HasPrefix(fmt.Sprint(in["file_name"]), "batch-") {
				id = "batch-folder"
			}
			write(map[string]string{"fid": id})
		case "/share/sharepage/save":
			saved.Add(1)
			write(map[string]string{"task_id": "save-task"})
		case "/task":
			write(map[string]any{"status": 2, "save_as": map[string]any{"save_as_top_fids": []string{"owned"}}})
		case "/file/download":
			write([]any{map[string]string{"download_url": upstream.URL + "/blob?secret=private-link"}})
		case "/blob":
			var start, end int
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			w.WriteHeader(206)
			w.Write(payload[start : end+1])
		default:
			t.Error("unexpected fixture request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	dataDir, root := t.TempDir(), t.TempDir()
	a, err := New(dataDir, root)
	if err != nil {
		t.Fatal(err)
	}
	a.newProvider = func(c string, cb func(string)) *quark.Client { return quark.NewAt(upstream.URL, c, cb) }
	sealed, err := a.seal("private-account-cookie")
	if err != nil {
		t.Fatal(err)
	}
	a.change(func(st *State) error {
		st.CookieCipher = sealed
		st.Settings.Sources = []string{"pansearch"}
		return nil
	})
	catalog, err := search.OpenCatalog(filepath.Join(dataDir, "resources.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalog.Remember([]search.Result{{Title: "demo video", URL: "https://pan.quark.cn/s/agentfixture"}, {Title: "demo invalid", URL: "https://pan.quark.cn/s/invalidfixture"}}, search.Source{ID: "pansearch", Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	a.search, err = search.NewPersistent(filepath.Join(dataDir, "resources.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.checkShare = func(ctx context.Context, url string) quark.ShareCheck {
		availability := "accessible"
		if strings.Contains(url, "invalidfixture") {
			availability = "invalid"
		}
		return quark.ShareCheck{URL: url, Availability: availability, CheckedAt: time.Now().UTC().Format(time.RFC3339), FileCount: 1}
	}
	a.sessions["admin-fixture"] = session{expires: time.Now().Add(time.Hour)}
	return a, dataDir, root, &saved
}

func TestAgentEndToEndConfirmationDownloadEventAndRestart(t *testing.T) {
	a, dataDir, root, saved := agentFixture(t)
	g, token := grantForTest(t, a, "OpenClaw", agentScopes)
	init := agentRequest(a, "POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": agentProtocolVersion}}, false)
	if init.Code != 200 || !strings.Contains(init.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatal(init.Code, init.Body.String())
	}
	list := agentRequest(a, "POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, false)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "panzhan_download") {
		t.Fatal(list.Body.String())
	}
	searchResult := agentRequest(a, "POST", "/api/agent/v1/search", token, map[string]any{"query": "demo", "local_only": true}, false)
	if searchResult.Code != 200 || !strings.Contains(searchResult.Body.String(), "agentfixture") || strings.Contains(searchResult.Body.String(), "invalidfixture") {
		t.Fatal(searchResult.Code, searchResult.Body.String())
	}
	missingContext := agentRequest(a, "POST", "/api/agent/v1/preview", token, map[string]any{"url": "https://pan.quark.cn/s/agentfixture"}, false)
	if missingContext.Code != 400 {
		t.Fatal("preview accepted no return conversation")
	}
	preview := agentRequest(a, "POST", "/api/agent/v1/preview", token, map[string]any{"url": "https://pan.quark.cn/s/agentfixture", "context": "wechat:owner"}, false)
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	var choice struct {
		ID string `json:"choice_id"`
	}
	json.Unmarshal(preview.Body.Bytes(), &choice)
	for _, secret := range []string{"private-share-token", "private-file-token", "private-account-cookie"} {
		if strings.Contains(preview.Body.String(), secret) {
			t.Error("preview leaked credential")
		}
	}
	unconfirmed := agentRequest(a, "POST", "/api/agent/v1/downloads", token, map[string]any{"choice_id": choice.ID, "request_id": "user-message-1", "user_confirmed": false}, false)
	if unconfirmed.Code != 400 || len(a.snapshot().Jobs) != 0 {
		t.Fatal("download created without explicit confirmation")
	}
	override := agentRequest(a, "POST", "/api/agent/v1/downloads", token, map[string]any{"choice_id": choice.ID, "request_id": "user-message-1", "user_confirmed": true, "download_root": "/other"}, false)
	if override.Code != 400 {
		t.Fatal("agent can override authorized directory")
	}
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 3)
	for n := 0; n < 3; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- agentRequest(a, "POST", "/api/agent/v1/downloads", token, map[string]any{"choice_id": choice.ID, "request_id": "user-message-1", "user_confirmed": true}, false)
		}()
	}
	wg.Wait()
	close(responses)
	for response := range responses {
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	st := a.snapshot()
	if len(st.Jobs) != 1 || st.Jobs[0].AgentID != g.ID || st.Jobs[0].AgentContext != "wechat:owner" {
		t.Fatal("duplicate or unattributed jobs", st.Jobs)
	}
	jobID := st.Jobs[0].ID
	other, otherToken := grantForTest(t, a, "Hermes", agentScopes)
	if response := agentRequest(a, "GET", "/api/agent/v1/tasks?task_id="+jobID, otherToken, nil, false); response.Code != 400 {
		t.Fatal("other grant can read task")
	}
	if response := agentRequest(a, "POST", "/api/agent/v1/downloads", otherToken, map[string]any{"choice_id": choice.ID, "request_id": "other-message", "user_confirmed": true}, false); response.Code != 400 {
		t.Fatal("other grant can consume choice", other.ID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		job, _ := a.job(jobID)
		if job.Status == "completed" {
			break
		}
		if job.Status == "attention" {
			t.Fatal(job.Error)
		}
		time.Sleep(15 * time.Millisecond)
	}
	job, _ := a.job(jobID)
	if job.Status != "completed" {
		t.Fatal("pipeline did not complete", job.Status)
	}
	if saved.Load() != 1 {
		t.Fatal("duplicate cloud save", saved.Load())
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(job.Directory), "demo.bin"))
	if err != nil || string(got) != "verified agent download bytes" {
		t.Fatal("incorrect published file", string(got), err)
	}
	if len(a.snapshot().AgentEvents) != 1 {
		t.Fatal("completion event missing or duplicated")
	}
	if err := a.update(jobID, func(j *Job) error { j.Status = "completed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(a.snapshot().AgentEvents) != 1 {
		t.Fatal("same state repeated notification")
	}
	restored, err := New(dataDir, root)
	if err != nil {
		t.Fatal(err)
	}
	events := agentRequest(restored, "GET", "/api/agent/v1/events", token, nil, false)
	if events.Code != 200 {
		t.Fatal(events.Code, events.Body.String())
	}
	var pending struct {
		Events []AgentEvent `json:"events"`
	}
	json.Unmarshal(events.Body.Bytes(), &pending)
	if len(pending.Events) != 1 || pending.Events[0].Context != "wechat:owner" || pending.Events[0].Status != "completed" {
		t.Fatal("restart lost notification", events.Body.String())
	}
	for _, secret := range []string{"private-account-cookie", "private-share-token", "private-link", "save-task"} {
		if strings.Contains(events.Body.String(), secret) {
			t.Fatal("event leaked credential")
		}
	}
	if response := agentRequest(restored, "POST", "/api/agent/v1/events/ack", otherToken, map[string]any{"event_id": pending.Events[0].ID}, false); response.Code != 400 {
		t.Fatal("other grant acknowledged event")
	}
	for n := 0; n < 2; n++ {
		ack := agentRequest(restored, "POST", "/api/agent/v1/events/ack", token, map[string]any{"event_id": pending.Events[0].ID}, false)
		if ack.Code != 200 {
			t.Fatal(ack.Body.String())
		}
	}
	after := agentRequest(restored, "GET", "/api/agent/v1/events", token, nil, false)
	if !strings.Contains(after.Body.String(), `"events":[]`) {
		t.Fatal(after.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "state.json"))
	if err != nil || bytes.Contains(data, []byte(token)) {
		t.Fatal("plaintext agent token persisted")
	}
}

func TestAgentAuthorizationAndMCPIsolation(t *testing.T) {
	a, _, _, _ := agentFixture(t)
	grant, token := grantForTest(t, a, "read-only", []string{"resources:read"})
	if response := agentRequest(a, "POST", "/api/agents", "", map[string]any{"name": "unauthorized", "scopes": agentScopes, "expires_days": 30}, false); response.Code != 401 {
		t.Fatal("anonymous grant creation")
	}
	if response := agentRequest(a, "GET", "/api/agents", token, nil, false); response.Code != 401 {
		t.Fatal("agent accessed admin APIs")
	}
	if response := agentRequest(a, "GET", "/api/account", token, nil, false); response.Code != 401 {
		t.Fatal("agent accessed credential API")
	}
	if response := agentRequest(a, "POST", "/api/agent/v1/downloads", token, map[string]any{"choice_id": "none", "request_id": "x", "user_confirmed": true}, false); response.Code != 403 {
		t.Fatal("read-only grant can download")
	}
	list := agentRequest(a, "POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}, false)
	if strings.Contains(list.Body.String(), "panzhan_download") || strings.Contains(list.Body.String(), "panzhan_tasks") {
		t.Fatal("tool list ignored scopes")
	}
	notification := agentRequest(a, "POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, false)
	if notification.Code != 202 || notification.Body.Len() != 0 {
		t.Fatal("notification transport response invalid")
	}
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"ping"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://unrelated.example")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin bearer accepted")
	}
	adminList := agentRequest(a, "GET", "/api/agents", "", nil, true)
	if adminList.Code != 200 || strings.Contains(adminList.Body.String(), "token_hash") || strings.Contains(adminList.Body.String(), token) {
		t.Fatal("grant list exposed token/hash")
	}
	revoke := agentRequest(a, "POST", "/api/agents/"+grant.ID, "", map[string]any{"action": "revoke"}, true)
	if revoke.Code != 200 {
		t.Fatal(revoke.Body.String())
	}
	if response := agentRequest(a, "GET", "/api/agent/v1/status", token, nil, false); response.Code != 401 {
		t.Fatal("revoked credential remained valid")
	}
	expired, expiredToken := grantForTest(t, a, "expired", agentScopes)
	a.change(func(st *State) error {
		for i := range st.Agents {
			if st.Agents[i].ID == expired.ID {
				st.Agents[i].Expires = time.Now().Add(-time.Second)
			}
		}
		return nil
	})
	if response := agentRequest(a, "GET", "/api/agent/v1/status", expiredToken, nil, false); response.Code != 401 {
		t.Fatal("expired credential remained valid")
	}
}

func TestAgentStateRollbackDoesNotRetainOmittedFields(t *testing.T) {
	a, _, _, _ := agentFixture(t)
	a.mu.Lock()
	originalDir := a.dataDir
	a.dataDir = filepath.Join(originalDir, "missing-parent", "state")
	a.mu.Unlock()
	err := a.change(func(st *State) error {
		st.Agents = []AgentGrant{{ID: "must-rollback"}}
		st.AgentChoices = []AgentChoice{{ID: "must-rollback"}}
		st.AgentEvents = []AgentEvent{{ID: 1}}
		st.AgentEventSeq = 1
		return nil
	})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	st := a.snapshot()
	if len(st.Agents) != 0 || len(st.AgentChoices) != 0 || len(st.AgentEvents) != 0 || st.AgentEventSeq != 0 {
		t.Fatal("failed transaction retained optional agent fields")
	}
	a.mu.Lock()
	a.dataDir = originalDir
	a.mu.Unlock()
}

func TestAgentChoiceExpiryAndIdempotencyConflict(t *testing.T) {
	a, _, _, _ := agentFixture(t)
	g, token := grantForTest(t, a, "agent", agentScopes)
	a.change(func(st *State) error {
		st.AgentChoices = []AgentChoice{{ID: "expired-choice", GrantID: g.ID, URL: "https://pan.quark.cn/s/agentfixture", Expires: time.Now().Add(-time.Second)}}
		return nil
	})
	r := agentRequest(a, "POST", "/api/agent/v1/downloads", token, map[string]any{"choice_id": "expired-choice", "request_id": "msg", "user_confirmed": true}, false)
	if r.Code != 400 {
		t.Fatal("expired selection created a task")
	}
	a.change(func(st *State) error {
		st.Jobs = []Job{{ID: "existing", AgentID: g.ID, AgentRequest: "msg", AgentChoice: "first", Status: "queued"}}
		return nil
	})
	r = agentRequest(a, "POST", "/api/agent/v1/downloads", token, map[string]any{"choice_id": "other", "request_id": "msg", "user_confirmed": true}, false)
	if r.Code != 400 || len(a.snapshot().Jobs) != 1 {
		t.Fatal("idempotency key reused for different resource")
	}
	response := agentRequest(a, "POST", "/mcp", token, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "panzhan_tasks", "arguments": map[string]any{}}}, false)
	data, err := io.ReadAll(response.Result().Body)
	if err != nil || !bytes.Contains(data, []byte("structuredContent")) {
		t.Fatal("MCP tool output malformed", string(data), err)
	}
}
