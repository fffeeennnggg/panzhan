package app

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
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
	http.SetCookie(w, &http.Cookie{Name: "panzhan_session", Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
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
	origin := r.Header.Get("Origin")
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (origin == "" || origin == scheme+"://"+r.Host) && r.Header.Get("Sec-Fetch-Site") != "cross-site" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}
func constantEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
