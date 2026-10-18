package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"panzhan/internal/quark"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCorruptSearchCatalogDoesNotDisableApplication(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	if _, err := New(dir, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources.json"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(dir, root)
	if err != nil {
		t.Fatalf("catalog corruption disabled app: %v", err)
	}
	if report := a.search.Local("Blender", a.snapshot().Settings.Sources); report.Warning == "" {
		t.Fatal("catalog problem was not surfaced")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "resources.json"))
	if string(data) != "damaged" {
		t.Fatal("corrupt catalog was overwritten")
	}
}

func TestExistingSourceSettingsGainPanSouPluginsWithoutReset(t *testing.T) {
	dir, err := os.MkdirTemp(filepath.Join("..", "..", ".tmp"), "source-migration-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "downloads")
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(st *State) error {
		st.Settings.Sources = []string{"pansearch", "telegram", "custom-source", "panplugin:pansearch"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	upgraded, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	got := upgraded.snapshot().Settings.Sources
	for _, id := range []string{"pansearch", "telegram", "custom-source", "panplugin:pansearch", "panplugin:quark4k", "panplugin:aipan"} {
		if !slices.Contains(got, id) {
			t.Errorf("upgraded sources missing %q", id)
		}
	}
	count := 0
	for _, id := range got {
		if id == "panplugin:pansearch" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("panplugin:pansearch appears %d times", count)
	}

	if _, err = New(dir, root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted State
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Settings.Sources) != len(got) {
		t.Fatal("source migration was not idempotent")
	}
}

func TestOldProviderCannotRestoreDisconnectedAccount(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "__puus", Value: "rotated-old"})
		w.Write([]byte(`{"status":200,"code":0,"data":{"list":[]}}`))
	}))
	defer s.Close()
	a, _ := New(t.TempDir(), t.TempDir())
	encrypted, _ := a.seal("__puus=old")
	a.change(func(st *State) error { st.CookieCipher = encrypted; return nil })
	a.newProvider = func(c string, cb func(string)) *quark.Client { return quark.NewAt(s.URL, c, cb) }
	old, err := a.provider()
	if err != nil {
		t.Fatal(err)
	}
	a.change(func(st *State) error { st.CookieCipher = ""; st.AccountRevision++; return nil })
	if err = old.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.snapshot().CookieCipher != "" {
		t.Fatal("old callback restored disconnected account")
	}
}

func TestSetupAuthenticationAndEncryptedCredentials(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	res, err := client.Get(srv.URL + "/api/tasks")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	token, _ := os.ReadFile(dir + "/setup-token.txt")
	post := func(path string, body any) *http.Response {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", srv.URL)
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	res = post("/api/setup", map[string]string{"token": strings.TrimSpace(string(token)), "password": "short7"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("accepted a password shorter than 8 characters: %d", res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/setup", map[string]string{"token": strings.TrimSpace(string(token)), "password": "qa-pass8"})
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	res = post("/api/account", map[string]string{"cookie": "__puus=secret-test-cookie"})
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	b, _ := os.ReadFile(dir + "/state.json")
	if bytes.Contains(b, []byte("secret-test-cookie")) || bytes.Contains(b, []byte("qa-pass8")) {
		t.Fatal("credentials in plaintext")
	}
	again, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if !again.isConfigured() {
		t.Fatal("lost password")
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/account", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.test")
	req.Header.Set("Content-Type", "application/json")
	res, _ = client.Do(req)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("accepted cross-origin", res.StatusCode)
	}
}

func TestInstallerPasswordBootstrapsLoginAndIsConsumed(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	if _, err := New(dir, root); err != nil {
		t.Fatal(err)
	}
	password := "fnos-8chars"
	if err := os.WriteFile(filepath.Join(dir, "bootstrap-password"), []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if !a.isConfigured() {
		t.Fatal("installer password was not activated")
	}
	for _, name := range []string{"bootstrap-password", "setup-token.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("one-time secret %s was not removed: %v", name, err)
		}
	}
	if state, err := os.ReadFile(filepath.Join(dir, "state.json")); err != nil || bytes.Contains(state, []byte(password)) {
		t.Fatal("installer password was not stored only as a hash")
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	requestBody, _ := json.Marshal(map[string]string{"password": password})
	req, _ := http.NewRequest("POST", server.URL+"/api/login", bytes.NewReader(requestBody))
	req.Header.Set("Origin", server.URL)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login with installer password returned %d", response.StatusCode)
	}
}
func TestPersistenceAndConstrainedDirectories(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	alternate := t.TempDir()
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(st *State) error {
		st.Settings.DefaultDir = "courses"
		st.Settings.DownloadRoot = alternate
		st.Jobs = append(st.Jobs, Job{ID: "restart", Status: "downloading"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if b.state.Settings.DefaultDir != "courses" || b.state.Jobs[0].Status != "queued" {
		t.Fatal(b.state)
	}
	if b.state.Settings.DownloadRoot != alternate || b.state.Jobs[0].DownloadRoot != root {
		t.Fatal("changing default destination moved a legacy task away from its original root")
	}
	if _, err = b.directory("../escape"); err == nil {
		t.Fatal("escaped root")
	}
	if _, err = b.directory("/absolute"); err == nil {
		t.Fatal("absolute escaped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.Run(ctx)
}

func TestValidateAlternateDownloadRootAndWriteAccess(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "videos")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := validateDownloadRoot(selected, true)
	if err != nil || got != selected {
		t.Fatalf("authorized alternate directory rejected: %q %v", got, err)
	}
	entries, err := os.ReadDir(selected)
	if err != nil || len(entries) != 0 {
		t.Fatalf("write probe was not cleaned up: %v %v", entries, err)
	}
	if _, err = validateDownloadRoot("relative/path", false); err == nil {
		t.Fatal("relative root accepted")
	}
	missing := filepath.Join(root, "missing")
	if _, err = validateDownloadRoot(missing, false); err == nil {
		t.Fatal("missing root accepted")
	}
}

func TestSettingsAndDirectoryBrowserUseAuthorizedAlternateRoot(t *testing.T) {
	data, original := t.TempDir(), t.TempDir()
	alternate := filepath.Join(t.TempDir(), "video-library")
	if err := os.Mkdir(alternate, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(alternate, "Films"), 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(data, original)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(s *State) error {
		s.Salt = "root-test"
		s.PasswordHash = passwordHash("test-pass8", s.Salt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	post := func(path string, value any) *http.Response {
		t.Helper()
		encoded, _ := json.Marshal(value)
		r, _ := http.NewRequest("POST", server.URL+path, bytes.NewReader(encoded))
		r.Header.Set("Origin", server.URL)
		r.Header.Set("Content-Type", "application/json")
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	response := post("/api/login", map[string]string{"password": "test-pass8"})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("login", response.StatusCode)
	}
	settings := a.snapshot().Settings
	settings.DownloadRoot = alternate
	response = post("/api/settings", settings)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("saving authorized root: %d", response.StatusCode)
	}
	if got := a.snapshot().Settings.DownloadRoot; got != alternate {
		t.Fatalf("saved root %q, want %q", got, alternate)
	}
	response, e := client.Get(server.URL + "/api/directories?root=" + url.QueryEscape(alternate))
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	var result struct {
		Root        string   `json:"root"`
		Directories []string `json:"directories"`
	}
	if e = json.NewDecoder(response.Body).Decode(&result); e != nil {
		t.Fatal(e)
	}
	if response.StatusCode != http.StatusOK || result.Root != alternate || !slices.Contains(result.Directories, "Films") {
		t.Fatalf("directory browse returned %+v (%d)", result, response.StatusCode)
	}
	response, e = client.Get(server.URL + "/api/directories?root=" + url.QueryEscape(filepath.Join(alternate, "..", "missing")))
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unauthorized path returned %d", response.StatusCode)
	}
}

func TestSettingsAPISelectsAnotherWritableShare(t *testing.T) {
	data, original := t.TempDir(), t.TempDir()
	alternate := filepath.Join(t.TempDir(), "documents")
	if err := os.Mkdir(alternate, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(data, original)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(s *State) error {
		s.Salt = "download-root-api"
		s.PasswordHash = passwordHash("root-pass8", s.Salt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar
	post := func(endpoint string, value any) *http.Response {
		t.Helper()
		encoded, _ := json.Marshal(value)
		r, _ := http.NewRequest("POST", server.URL+endpoint, bytes.NewReader(encoded))
		r.Header.Set("Origin", server.URL)
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	response := post("/api/login", map[string]string{"password": "root-pass8"})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("login", response.StatusCode)
	}
	settings := a.snapshot().Settings
	settings.DownloadRoot = alternate
	response = post("/api/settings", settings)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save alternate path: %d", response.StatusCode)
	}
	if a.snapshot().Settings.DownloadRoot != alternate {
		t.Fatal("alternate path was not persisted")
	}
	response, err = client.Get(server.URL + "/api/directories?root=" + url.QueryEscape(alternate))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("browse alternate path: %d", response.StatusCode)
	}
}

func TestMissingMasterKeyDoesNotSilentlyReplaceEncryptedState(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := a.seal("__puus=secret")
	a.change(func(s *State) error { s.CookieCipher = cipher; return nil })
	os.Remove(dir + "/master.key")
	if _, err = New(dir, root); err == nil {
		t.Fatal("accepted encrypted state without original key")
	}
	if _, err = os.Stat(dir + "/master.key"); !os.IsNotExist(err) {
		t.Fatal("must not create replacement key")
	}
}

func TestMissingStateDoesNotSilentlyResetInstallation(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	if _, err := New(dir, root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir + "/state.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, root); err == nil {
		t.Fatal("accepted existing key without matching state")
	}
	if _, err := os.Stat(dir + "/state.json"); !os.IsNotExist(err) {
		t.Fatal("must not recreate empty state")
	}
}
