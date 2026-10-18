package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func installerTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	data, root := t.TempDir(), t.TempDir()
	a, err := New(data, root)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := a.seal("offline-fixture-cookie")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(s *State) error {
		s.Salt = "installer-test"
		s.PasswordHash = passwordHash("old-pass8", s.Salt)
		s.CookieCipher = cipher
		s.AccountRevision = 17
		s.Settings.Concurrency = 6
		s.Jobs = []Job{{ID: "keep-this-task", Title: "existing download", Status: "completed", Directory: "existing", DownloadRoot: root}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a, data, root
}

func installerLoginStatus(a *App, password string) int {
	body, _ := json.Marshal(map[string]string{"password": password})
	r := httptest.NewRequest("POST", "/api/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w.Code
}

func TestInstallerPasswordReplacesPreservedPassword(t *testing.T) {
	for name, password := range map[string]string{
		"ASCII":                  "new-pass8",
		"Unicode":                "中文密码测试八个字符",
		"spaces and punctuation": "  new$pass8!  ",
	} {
		t.Run(name, func(t *testing.T) {
			old, data, root := installerTestApp(t)
			before := old.snapshot()
			key, _ := os.ReadFile(filepath.Join(data, "master.key"))
			if err := os.WriteFile(filepath.Join(data, "bootstrap-password"), []byte(password+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			a, err := New(data, root)
			if err != nil {
				t.Fatal(err)
			}
			if got := installerLoginStatus(a, password); got != http.StatusOK {
				t.Fatalf("wizard-selected password was ignored with preserved state: login status %d", got)
			}
			if got := installerLoginStatus(a, "old-pass8"); got != http.StatusUnauthorized {
				t.Fatalf("old password still accepted after explicit wizard update: %d", got)
			}
			after := a.snapshot()
			if !reflect.DeepEqual(before.Settings, after.Settings) || !reflect.DeepEqual(before.Jobs, after.Jobs) || before.CookieCipher != after.CookieCipher || before.AccountRevision != after.AccountRevision {
				t.Fatal("password update changed account, settings, or tasks")
			}
			keyAfter, _ := os.ReadFile(filepath.Join(data, "master.key"))
			if !bytes.Equal(key, keyAfter) {
				t.Fatal("password update replaced the account encryption key")
			}
			if cookie, err := a.unseal(after.CookieCipher); err != nil || cookie != "offline-fixture-cookie" {
				t.Fatal("existing account could no longer be decrypted")
			}
			for _, name := range []string{"bootstrap-password", "setup-token.txt"} {
				if _, err := os.Stat(filepath.Join(data, name)); !os.IsNotExist(err) {
					t.Errorf("one-time setup file remains: %s", name)
				}
			}
		})
	}
}

func TestRestartWithoutWizardPasswordPreservesPassword(t *testing.T) {
	_, data, root := installerTestApp(t)
	a, err := New(data, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := installerLoginStatus(a, "old-pass8"); got != http.StatusOK {
		t.Fatalf("normal restart changed the saved password: %d", got)
	}
}

func TestInvalidWizardPasswordDoesNotReplaceSavedHash(t *testing.T) {
	old, data, root := installerTestApp(t)
	hash := old.snapshot().PasswordHash
	if err := os.WriteFile(filepath.Join(data, "bootstrap-password"), []byte("short\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(data, root); err == nil {
		t.Fatal("invalid staged password was silently discarded")
	}
	var saved State
	b, _ := os.ReadFile(filepath.Join(data, "state.json"))
	if err := json.Unmarshal(b, &saved); err != nil || saved.PasswordHash != hash {
		t.Fatal("invalid password damaged the existing hash")
	}
}
