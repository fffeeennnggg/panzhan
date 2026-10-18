// SPDX-License-Identifier: AGPL-3.0-or-later
package quark

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ShareCheck never exposes the temporary share token or account credentials.
type ShareCheck struct {
	URL          string          `json:"url"`
	Availability string          `json:"availability"`
	CheckedAt    string          `json:"checked_at"`
	Message      string          `json:"check_message"`
	FileCount    int             `json:"file_count,omitempty"`
	Content      *ContentSummary `json:"content,omitempty"`
}

type cachedCheck struct {
	result ShareCheck
	until  time.Time
}
type ShareChecker struct {
	client                *http.Client
	tokenBase, detailBase string
	slots                 chan struct{}
	mu                    sync.Mutex
	cache                 map[string]cachedCheck
	flight                singleflight.Group
}

func NewShareChecker() *ShareChecker {
	c := newShareCheckerAt("https://drive-pc.quark.cn/1/clouddrive")
	c.tokenBase = "https://drive-h.quark.cn/1/clouddrive"
	return c
}

func newShareCheckerAt(base string) *ShareChecker {
	return &ShareChecker{client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, tokenBase: base, detailBase: base, slots: make(chan struct{}, 3), cache: map[string]cachedCheck{}}
}

func (c *ShareChecker) Check(ctx context.Context, raw string) ShareCheck {
	out := ShareCheck{URL: raw, Availability: "unknown", Message: "暂时无法验证"}
	s, err := ParseShare(raw)
	if err != nil {
		out.Message = "分享地址无法识别"
		return out
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "pan.quark.cn") || u.User != nil {
		return out
	}
	key := s.ID + ":" + s.Parent + ":" + s.Code
	c.mu.Lock()
	cached, ok := c.cache[key]
	c.mu.Unlock()
	if ok && time.Now().Before(cached.until) {
		cached.result.URL = raw
		return cached.result
	}
	// Share checks are public reads. Do not send the user's account Cookie.
	ch := c.flight.DoChan(key, func() (any, error) {
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return out, ctx.Err()
		}
		defer func() { <-c.slots }()
		child, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		result := c.probe(child, s)
		result.URL = raw
		if child.Err() == nil {
			ttl := time.Minute
			if result.Availability == "accessible" {
				ttl = 30 * time.Minute
			}
			if result.Availability == "invalid" {
				ttl = 10 * time.Minute
			}
			c.mu.Lock()
			if len(c.cache) >= 2048 {
				for k := range c.cache {
					delete(c.cache, k)
					break
				}
			}
			c.cache[key] = cachedCheck{result: result, until: time.Now().Add(ttl)}
			c.mu.Unlock()
		}
		return result, nil
	})
	select {
	case <-ctx.Done():
		return out
	case r := <-ch:
		if r.Err != nil {
			return out
		}
		result := r.Val.(ShareCheck)
		result.URL = raw
		return result
	}
}

type checkEnvelope struct {
	Status   int             `json:"status"`
	Code     int             `json:"code"`
	Data     json.RawMessage `json:"data"`
	Metadata struct {
		Total int `json:"_total"`
	} `json:"metadata"`
}

func (c *ShareChecker) request(ctx context.Context, method, endpoint string, payload any) (checkEnvelope, bool) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return checkEnvelope{}, false
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return checkEnvelope{}, false
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Referer", "https://pan.quark.cn/")
	req.Header.Set("Origin", "https://pan.quark.cn")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client.Do(req)
	if err != nil {
		return checkEnvelope{}, false
	}
	defer res.Body.Close()
	// Quark returns definitive share error codes in JSON with HTTP 400/404.
	// A plain HTTP 404, login error or rate limit alone is not proof of expiry.
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
		return checkEnvelope{}, false
	}
	var out checkEnvelope
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&out) != nil {
		return out, false
	}
	if res.StatusCode != http.StatusOK && out.Code == 0 {
		return out, false
	}
	return out, true
}

func checkFailure(e checkEnvelope) (string, string) {
	switch e.Code {
	case 41008:
		return "password_required", "需要正确的提取码"
	// 41012 (cancelled) and 41006 (missing) were confirmed against live
	// responses; PanSou's original checker did not classify these codes.
	case 41004, 41006, 41010, 41011, 41012:
		return "invalid", "分享已取消、过期或不存在"
	}
	return "unknown", "暂时无法验证，可能受网络或访问限制影响"
}

func (c *ShareChecker) probe(ctx context.Context, s Share) ShareCheck {
	out := ShareCheck{Availability: "unknown", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Message: "暂时无法验证，可能受网络或访问限制影响"}
	endpoint := c.tokenBase + "/share/sharepage/token?pr=ucpro&fr=pc"
	e, ok := c.request(ctx, "POST", endpoint, map[string]any{"pwd_id": s.ID, "passcode": s.Code, "support_visit_limit_private_share": true})
	if !ok {
		return out
	}
	if e.Code != 0 || (e.Status != 0 && e.Status != 200) {
		out.Availability, out.Message = checkFailure(e)
		return out
	}
	var token struct {
		Token string `json:"stoken"`
	}
	if json.Unmarshal(e.Data, &token) != nil || token.Token == "" {
		return out
	}
	q := url.Values{"pwd_id": {s.ID}, "stoken": {token.Token}, "pdir_fid": {s.Parent}, "_page": {"1"}, "_size": {"100"}, "_fetch_total": {"1"}, "_fetch_share": {"1"}, "ver": {"2"}, "pr": {"ucpro"}, "fr": {"pc"}}
	e, ok = c.request(ctx, "GET", c.detailBase+"/share/sharepage/detail?"+q.Encode(), nil)
	if !ok {
		return out
	}
	if e.Code != 0 || (e.Status != 0 && e.Status != 200) {
		out.Availability, out.Message = checkFailure(e)
		return out
	}
	var detail struct {
		List     []summaryFile `json:"list"`
		IsExpire bool          `json:"is_expire"`
		Share    struct {
			Status           int  `json:"status"`
			PartialViolation bool `json:"partial_violation"`
		} `json:"share"`
	}
	if json.Unmarshal(e.Data, &detail) != nil {
		return out
	}
	if detail.IsExpire || (detail.Share.Status > 1 && (detail.Share.Status != 3 || detail.Share.PartialViolation)) {
		out.Availability, out.Message = "invalid", "分享已过期或被禁用"
		return out
	}
	// An empty folder alone does not prove that the share was deleted.
	if len(detail.List) == 0 {
		out.Message = "分享目录为空，暂未确认可用文件"
		return out
	}
	out.Availability, out.Message, out.FileCount = "accessible", "已验证可读取", e.Metadata.Total
	out.Content = c.summarize(ctx, q, detail.List, e.Metadata.Total)
	return out
}
