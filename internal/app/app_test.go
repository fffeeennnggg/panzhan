package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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
	res = post("/api/setup", map[string]string{"token": strings.TrimSpace(string(token)), "password": "a-strong-password"})
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
	if bytes.Contains(b, []byte("secret-test-cookie")) || bytes.Contains(b, []byte("a-strong-password")) {
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
func TestPersistenceAndConstrainedDirectories(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a, err := New(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(st *State) error {
		st.Settings.DefaultDir = "courses"
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
