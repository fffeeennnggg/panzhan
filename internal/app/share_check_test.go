package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"panzhan/internal/quark"
	"strings"
	"testing"
	"time"
)

func TestShareCheckAPIRequiresLoginLimitsBatchesAndKeepsUnknown(t *testing.T) {
	a, err := New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.sessions["check-test"] = session{expires: time.Now().Add(time.Hour)}
	a.checkShare = func(_ context.Context, raw string) quark.ShareCheck {
		return quark.ShareCheck{URL: raw, Availability: "unknown", Message: "network unavailable"}
	}
	request := func(body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/search/check", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "panzhan_session", Value: "check-test"})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request(`{"urls":["https://pan.quark.cn/s/abc"]}`, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"urls":[]}`, `{"urls":["1","2","3","4","5","6","7","8","9"]}`} {
		if w := request(body, true); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	w := request(`{"urls":["https://pan.quark.cn/s/abc"]}`, true)
	var got struct {
		Checks []quark.ShareCheck `json:"checks"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Checks) != 1 || got.Checks[0].Availability != "unknown" {
		t.Fatal(w.Code, w.Body.String())
	}
}
