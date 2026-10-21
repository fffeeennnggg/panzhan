// SPDX-License-Identifier: AGPL-3.0-or-later
// Protocol adapted from QAS and AList Quark driver (Cookie snapshot and original-file link).
package quark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.4-b478491100 Safari/537.36 Channel/pckk_other_ch"

type File struct {
	ID     string `json:"fid"`
	Name   string `json:"file_name"`
	Size   int64  `json:"size"`
	IsFile bool   `json:"file"`
	Token  string `json:"share_fid_token,omitempty"`
	MD5    string `json:"md5,omitempty"`
}
type Link struct {
	URL    string
	Header http.Header
}
type Client struct {
	base     string
	http     *http.Client
	mu       sync.Mutex
	cookie   string
	onCookie func(string)
}

type TemporaryError struct{ Err error }

func (e *TemporaryError) Error() string { return e.Err.Error() }
func (e *TemporaryError) Unwrap() error { return e.Err }

func New(cookie string, onCookie func(string)) *Client {
	return NewAt("https://drive-pc.quark.cn/1/clouddrive", cookie, onCookie)
}
func NewAt(base, cookie string, onCookie func(string)) *Client {
	return &Client{base: strings.TrimRight(base, "/"), cookie: cookie, onCookie: onCookie, http: &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Cookie() string { c.mu.Lock(); defer c.mu.Unlock(); return c.cookie }
func (c *Client) mergeCookies(cookies []*http.Cookie) {
	c.mu.Lock()
	req := &http.Request{Header: http.Header{"Cookie": {c.cookie}}}
	values := map[string]string{}
	for _, v := range req.Cookies() {
		values[v.Name] = v.Value
	}
	changed := false
	for _, v := range cookies {
		if v.Name == "__puus" || v.Name == "__pus" {
			values[v.Name] = v.Value
			changed = true
		}
	}
	if changed {
		var parts []string
		for k, v := range values {
			parts = append(parts, k+"="+v)
		}
		c.cookie = strings.Join(parts, "; ")
	}
	latest := c.cookie
	c.mu.Unlock()
	if changed && c.onCookie != nil {
		c.onCookie(latest)
	}
}

type envelope struct {
	Status   int             `json:"status"`
	Code     int             `json:"code"`
	Data     json.RawMessage `json:"data"`
	Metadata struct {
		Total int `json:"_total"`
	} `json:"metadata"`
}

func (c *Client) request(ctx context.Context, method, path string, params url.Values, body any, cookie string) (envelope, error) {
	var out envelope
	if params == nil {
		params = url.Values{}
	}
	params.Set("pr", "ucpro")
	params.Set("fr", "pc")
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return out, err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path+"?"+params.Encode(), payload)
	if err != nil {
		return out, errors.New("夸克请求构造失败")
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Referer", "https://pan.quark.cn")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, &TemporaryError{Err: errors.New("夸克请求未收到响应，请检查网络或核对任务状态")}
	}
	defer res.Body.Close()
	c.mergeCookies(res.Cookies())
	if res.StatusCode != 200 {
		if res.StatusCode == http.StatusNotFound && strings.HasPrefix(path, "/share/sharepage/") {
			return out, errors.New("夸克返回 HTTP 404：分享可能已失效、已被删除或当前账号无权访问")
		}
		message := fmt.Errorf("夸克 HTTP %d，请检查账号或网络", res.StatusCode)
		if res.StatusCode == http.StatusRequestTimeout || res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			return out, &TemporaryError{Err: message}
		}
		return out, message
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out); err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		var networkError net.Error
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkError) {
			return out, &TemporaryError{Err: errors.New("夸克响应中断，将自动重试")}
		}
		return out, errors.New("夸克返回格式无法解析")
	}
	if out.Code != 0 || out.Status >= 400 {
		message := fmt.Errorf("夸克拒绝请求（状态 %d，代码 %d），请检查账号、分享及空间", out.Status, out.Code)
		if out.Status == http.StatusRequestTimeout || out.Status == http.StatusTooManyRequests || out.Status >= 500 {
			return out, &TemporaryError{Err: message}
		}
		return out, message
	}
	return out, nil
}
func (c *Client) call(ctx context.Context, method, path string, q url.Values, b any, v any) (envelope, error) {
	e, err := c.request(ctx, method, path, q, b, c.Cookie())
	if err != nil {
		return e, err
	}
	if v != nil {
		err = json.Unmarshal(e.Data, v)
		if err != nil {
			return e, errors.New("夸克数据结构不兼容")
		}
	}
	return e, err
}
func (c *Client) Token(ctx context.Context, s Share) (string, error) {
	var d struct {
		Token string `json:"stoken"`
	}
	_, err := c.call(ctx, "POST", "/share/sharepage/token", nil, map[string]string{"pwd_id": s.ID, "passcode": s.Code}, &d)
	if err == nil && d.Token == "" {
		err = errors.New("分享令牌为空，请检查分享或提取码")
	}
	return d.Token, err
}
func (c *Client) list(ctx context.Context, path string, q url.Values) ([]File, error) {
	files := []File{}
	for page := 1; page <= 10000; page++ {
		q.Set("_page", strconv.Itoa(page))
		q.Set("_size", "100")
		q.Set("_fetch_total", "1")
		q.Set("_sort", "file_type:asc,file_name:asc")
		var d struct {
			List []File `json:"list"`
		}
		e, err := c.call(ctx, "GET", path, q, nil, &d)
		if err != nil {
			return nil, err
		}
		files = append(files, d.List...)
		if len(d.List) == 0 || (e.Metadata.Total > 0 && len(files) >= e.Metadata.Total) || len(d.List) < 100 {
			return files, nil
		}
	}
	return nil, errors.New("文件数量超过安全分页上限")
}
func (c *Client) ShareFiles(ctx context.Context, s Share, token string) ([]File, error) {
	return c.list(ctx, "/share/sharepage/detail", url.Values{"pwd_id": {s.ID}, "stoken": {token}, "pdir_fid": {s.Parent}, "force": {"0"}, "_fetch_share": {"1"}, "ver": {"2"}})
}
func (c *Client) Files(ctx context.Context, parent string) ([]File, error) {
	return c.list(ctx, "/file/sort", url.Values{"pdir_fid": {parent}, "fetch_all_file": {"1"}, "fetch_risk_file_name": {"1"}})
}
func (c *Client) Verify(ctx context.Context) error { _, err := c.Files(ctx, "0"); return err }
func (c *Client) EnsureFolder(ctx context.Context, parent, name string) (string, error) {
	fs, err := c.Files(ctx, parent)
	if err != nil {
		return "", err
	}
	for _, f := range fs {
		if f.Name == name && !f.IsFile {
			return f.ID, nil
		}
	}
	var d struct {
		ID string `json:"fid"`
	}
	_, err = c.call(ctx, "POST", "/file", nil, map[string]any{"pdir_fid": parent, "file_name": name, "dir_path": "", "dir_init_lock": false}, &d)
	if err == nil && d.ID == "" {
		err = errors.New("云端目录创建结果为空")
	}
	return d.ID, err
}
func (c *Client) StartSave(ctx context.Context, s Share, token string, files []File, dest string) (string, error) {
	if len(files) == 0 || len(files) > 100 {
		return "", errors.New("转存批次必须包含 1–100 个项目")
	}
	ids, tokens := []string{}, []string{}
	for _, f := range files {
		ids = append(ids, f.ID)
		tokens = append(tokens, f.Token)
	}
	var d struct {
		ID string `json:"task_id"`
	}
	_, err := c.call(ctx, "POST", "/share/sharepage/save", nil, map[string]any{"fid_list": ids, "fid_token_list": tokens, "to_pdir_fid": dest, "pwd_id": s.ID, "stoken": token, "pdir_fid": s.Parent, "scene": "link"}, &d)
	if err == nil && d.ID == "" {
		err = errors.New("转存未返回任务 ID，请核对云端目录")
	}
	return d.ID, err
}
func (c *Client) SaveStatus(ctx context.Context, id string) (bool, []string, error) {
	var d struct {
		Status int `json:"status"`
		SaveAs struct {
			IDs []string `json:"save_as_top_fids"`
		} `json:"save_as"`
	}
	_, err := c.call(ctx, "GET", "/task", url.Values{"task_id": {id}, "retry_index": {"0"}}, nil, &d)
	if err != nil {
		return false, nil, err
	}
	if d.Status > 2 || d.Status < 0 {
		return false, nil, errors.New("云端转存任务失败，请检查空间和分享状态")
	}
	return d.Status == 2, d.SaveAs.IDs, nil
}
func (c *Client) DownloadLink(ctx context.Context, id string) (Link, error) {
	snapshot := c.Cookie()
	e, err := c.request(ctx, "POST", "/file/download", nil, map[string]any{"fids": []string{id}}, snapshot)
	if err != nil {
		return Link{}, err
	}
	var data []struct {
		URL string `json:"download_url"`
	}
	if json.Unmarshal(e.Data, &data) != nil || len(data) == 0 || data[0].URL == "" {
		return Link{}, errors.New("夸克未返回原文件下载地址")
	}
	return Link{URL: data[0].URL, Header: http.Header{"Cookie": {snapshot}, "Referer": {"https://pan.quark.cn"}, "User-Agent": {UserAgent}}}, nil
}
