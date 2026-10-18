package app

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func passwordHash(password, salt string) string {
	key, err := pbkdf2.Key(sha256.New, password, []byte(salt), 210000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(key)
}
func (a *App) authenticated(r *http.Request) bool {
	c, err := r.Cookie("panzhan_session")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[c.Value]
	return ok && time.Now().Before(s.expires)
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	token := randomID() + randomID()
	a.mu.Lock()
	for k, s := range a.sessions {
		if time.Now().After(s.expires) {
			delete(a.sessions, k)
		}
	}
	a.sessions[token] = session{time.Now().Add(12 * time.Hour)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "panzhan_session", Value: token, Path: "/", HttpOnly: true, Secure: isHTTPSRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: 43200})
}

func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return !strings.Contains(r.Header.Get("X-Forwarded-Proto"), ",") && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}
func (a *App) rateLimited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	recent := a.failures[:0]
	for _, v := range a.failures {
		if time.Since(v) < time.Minute {
			recent = append(recent, v)
		}
	}
	a.failures = append(recent, time.Now())
	return len(recent) >= 10
}
func sameOrigin(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme, host := "http", r.Host
	if isHTTPSRequest(r) {
		scheme = "https"
	}
	forwardedProto := r.Header.Get("X-Forwarded-Proto")
	forwardedHost := r.Header.Get("X-Forwarded-Host")
	if !strings.Contains(forwardedProto, ",") && !strings.Contains(forwardedHost, ",") && forwardedHost != "" {
		proto := strings.ToLower(strings.TrimSpace(forwardedProto))
		if proto == "http" || proto == "https" {
			scheme, host = proto, strings.TrimSpace(forwardedHost)
		}
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, host)
}

// WebViews and reverse proxies may expose an origin that differs from Host.
// A page must first read this token from our no-store, same-origin status API.
// Cross-origin pages cannot read it or send the custom header without CORS.
func (a *App) validRequestToken(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json" && a.requestToken != "" && constantEqual(r.Header.Get("X-Panzhan-Request-Token"), a.requestToken)
}
func constantEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
