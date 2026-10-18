package quark

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Opt-in read-only probe. Never include share tokens or account cookies in logs.
func TestLiveShareChecks(t *testing.T) {
	raw := os.Getenv("PANZHAN_LIVE_CHECK_URLS")
	if raw == "" {
		t.Skip("live share checks are opt-in")
	}
	expected := strings.Split(os.Getenv("PANZHAN_LIVE_CHECK_EXPECTED"), ",")
	c := NewShareChecker()
	for i, raw := range strings.Fields(raw) {
		got := c.Check(context.Background(), raw)
		t.Logf("share %d: state=%s message=%s", i+1, got.Availability, got.Message)
		if got.Content != nil {
			t.Logf("content: files=%d bytes=%d complete=%t types=%v", got.Content.FileCount, got.Content.TotalBytes, got.Content.Complete, got.Content.Types)
		}
		if i < len(expected) && expected[i] != "" && got.Availability != expected[i] {
			t.Errorf("share %d: want %s, got %s", i+1, expected[i], got.Availability)
		}
	}
}

func TestPublicShareCheckClassifiesOnlyDefinitiveFailures(t *testing.T) {
	for _, tc := range []struct {
		name, token, detail, want string
		status                    int
	}{
		{"readable", `{"status":200,"code":0,"data":{"stoken":"secret"}}`, `{"status":200,"code":0,"data":{"list":[{"fid":"a"}],"share":{"status":1}},"metadata":{"_total":12}}`, "accessible", 200},
		{"expired code", `{"status":400,"code":41004}`, "", "invalid", 200},
		{"cancelled", `{"status":400,"code":41010}`, "", "invalid", 200},
		{"deleted", `{"status":400,"code":41011}`, "", "invalid", 200},
		{"live cancelled code", `{"status":404,"code":41012}`, "", "invalid", 200},
		{"live missing code", `{"status":404,"code":41006}`, "", "invalid", 200},
		{"http404 cancelled", `{"status":404,"code":41012}`, "", "invalid", 404},
		{"http404 missing", `{"status":404,"code":41006}`, "", "invalid", 404},
		{"password", `{"status":400,"code":41008}`, "", "password_required", 200},
		{"login error", `{"status":401,"code":41001}`, "", "unknown", 200},
		{"rate limit", `{}`, "", "unknown", 429},
		{"http404", `{}`, "", "unknown", 404},
		{"malformed", `<html>blocked</html>`, "", "unknown", 200},
		{"empty token", `{"status":200,"code":0,"data":{}}`, "", "unknown", 200},
		{"detail expired", `{"data":{"stoken":"secret"}}`, `{"data":{"is_expire":true}}`, "invalid", 200},
		{"empty folder", `{"data":{"stoken":"secret"}}`, `{"data":{"list":[],"share":{"status":1}}}`, "unknown", 200},
		{"partial violation", `{"data":{"stoken":"secret"}}`, `{"data":{"list":[{}],"share":{"status":3,"partial_violation":true}}}`, "invalid", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Cookie") != "" {
					t.Error("public checks sent account Cookie")
				}
				w.WriteHeader(tc.status)
				if r.Method == "POST" {
					fmt.Fprint(w, tc.token)
				} else {
					fmt.Fprint(w, tc.detail)
				}
			}))
			defer srv.Close()
			c := newShareCheckerAt(srv.URL)
			got := c.Check(context.Background(), "https://pan.quark.cn/s/abc")
			if got.Availability != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
			before := hits.Load()
			c.Check(context.Background(), "https://pan.quark.cn/s/abc")
			if hits.Load() != before {
				t.Fatal("check cache was not reused")
			}
			if tc.name == "readable" && got.FileCount != 12 {
				t.Fatal(got)
			}
		})
	}
}

func TestShareCheckRejectsForeignHostsWithoutNetwork(t *testing.T) {
	c := newShareCheckerAt("http://127.0.0.1:1")
	for _, raw := range []string{"https://evil.test/s/abc", "https://pan.quark.cn.evil.test/s/abc", "https://user@pan.quark.cn/s/abc", "http://pan.quark.cn/s/abc"} {
		if got := c.Check(context.Background(), raw); got.Availability != "unknown" || got.CheckedAt != "" {
			t.Fatal(got)
		}
	}
}
