package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSameOriginAcceptsFnOSReverseProxyOrigin(t *testing.T) {
	r := httptest.NewRequest("POST", "http://127.0.0.1:9867/api/login", nil)
	r.Header.Set("Origin", "https://nas.example:5667")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "nas.example:5667")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if !sameOrigin(r) {
		t.Fatal("same-origin request through fnOS reverse proxy was rejected")
	}
	if !isHTTPSRequest(r) {
		t.Fatal("HTTPS reverse proxy was not recognized for secure session cookies")
	}
}

func TestSetupThroughFnOSProxySetsSecureSessionCookie(t *testing.T) {
	data, root := t.TempDir(), t.TempDir()
	app, err := New(data, root)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := []byte("proxy-pass8\n")
	if err = os.WriteFile(filepath.Join(data, "bootstrap-password"), bootstrap, 0600); err != nil {
		t.Fatal(err)
	}
	app, err = New(data, root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	body, _ := json.Marshal(map[string]string{"password": "proxy-pass8"})
	req, _ := http.NewRequest("POST", server.URL+"/api/login", bytes.NewReader(body))
	req.Host = "127.0.0.1:9867"
	req.Header.Set("Origin", "https://nas.example:5667")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "nas.example:5667")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reverse-proxy login returned %d", response.StatusCode)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "panzhan_session" {
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatalf("proxy cookie lost security attributes: %+v", cookie)
			}
			return
		}
	}
	t.Fatal("login did not set a session cookie")
}

func TestSameOriginAcceptsDirectHTTPAndAbsentOrigin(t *testing.T) {
	r := httptest.NewRequest("POST", "http://nas.local:9867/api/login", nil)
	r.Header.Set("Content-Type", "application/json")
	if !sameOrigin(r) {
		t.Fatal("origin-less non-browser API request should remain supported")
	}
	r.Header.Set("Origin", "http://NAS.local:9867")
	if !sameOrigin(r) {
		t.Fatal("case-insensitive same-origin host was rejected")
	}
}

func TestWebViewLoginUsesPageRequestToken(t *testing.T) {
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.change(func(s *State) error { s.Salt = "test"; s.PasswordHash = passwordHash("qa-pass8", s.Salt); return nil }); err != nil {
		t.Fatal(err)
	}
	status := httptest.NewRecorder()
	a.Handler().ServeHTTP(status, httptest.NewRequest("GET", "/api/status", nil))
	var page struct {
		Token string `json:"request_token"`
	}
	if err = json.Unmarshal(status.Body.Bytes(), &page); err != nil || page.Token == "" {
		t.Fatal("status did not supply the page request token")
	}
	for _, tc := range []struct {
		name, token, content string
		want                 int
	}{
		{"mobile WebView", page.Token, "application/json; charset=utf-8", 200},
		{"missing page token", "", "application/json", 403},
		{"wrong page token", "wrong", "application/json", 403},
		{"wrong content type", page.Token, "text/plain", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/login", bytes.NewBufferString(`{"password":"qa-pass8"}`))
			r.Header.Set("Origin", "null")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Content-Type", tc.content)
			r.Header.Set("X-Panzhan-Request-Token", tc.token)
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, r)
			if response.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

func TestSameOriginRejectsSpoofedProxyAndCrossSiteRequests(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"forwarded host mismatch": {"Origin": "https://nas.example", "X-Forwarded-Proto": "https", "X-Forwarded-Host": "other.example", "Content-Type": "application/json"},
		"cross site":              {"Origin": "http://127.0.0.1:9867", "Sec-Fetch-Site": "cross-site", "Content-Type": "application/json"},
		"non JSON":                {"Origin": "http://127.0.0.1:9867", "Sec-Fetch-Site": "same-origin", "Content-Type": "text/plain"},
		"JSON prefix is not JSON": {"Origin": "http://127.0.0.1:9867", "Sec-Fetch-Site": "same-origin", "Content-Type": "application/jsonp"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:9867/api/login", nil)
			for key, value := range headers {
				r.Header.Set(key, value)
			}
			if sameOrigin(r) {
				t.Fatal("invalid request passed origin validation")
			}
		})
	}
}
