// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

func (e *Engine) searchQupansou(ctx context.Context, source Source, keyword string) ([]Result, error) {
	endpoint := strings.TrimRight(source.Base, "/") + "/search"
	body := map[string]any{
		"style": "get", "datasrc": "search",
		"query": map[string]any{"id": "", "datetime": "", "courseid": 1, "categoryid": "", "filetypeid": "", "filetype": "", "reportid": "", "validid": "", "searchtext": keyword},
		"page":  map[string]any{"pageSize": 1000, "pageIndex": 1},
		"order": map[string]any{"prop": "sort", "order": "desc"}, "message": "请求资源列表数据",
	}
	response, err := e.postJSON(ctx, endpoint, "https://pan.funletu.com/", body)
	if err != nil {
		return nil, err
	}
	var data struct {
		Status int `json:"status"`
		Data   []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			ExtCode string `json:"extcode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &data); err != nil {
		return nil, fmt.Errorf("夸克搜响应无法解析")
	}
	if data.Status != 200 {
		return nil, fmt.Errorf("夸克搜返回状态 %d", data.Status)
	}
	results := make([]Result, 0, len(data.Data))
	for _, item := range data.Data {
		link := item.URL
		if item.ExtCode != "" && !strings.Contains(link, "pwd=") {
			link += "?pwd=" + url.QueryEscape(item.ExtCode)
		}
		if result, ok := quarkResult(item.Title, link, source.Name); ok {
			results = append(results, result)
		}
	}
	return results, nil
}

func (e *Engine) searchJikepan(ctx context.Context, source Source, keyword string) ([]Result, error) {
	endpoint := strings.TrimRight(source.Base, "/") + "/search"
	response, err := e.postJSON(ctx, endpoint, "https://jikepan.xyz/", map[string]any{"name": keyword, "is_all": false})
	if err != nil {
		return nil, err
	}
	var data struct {
		Msg  string `json:"msg"`
		List []struct {
			Name  string `json:"name"`
			Links []struct {
				Service  string `json:"service"`
				Link     string `json:"link"`
				Password string `json:"pwd"`
			} `json:"links"`
		} `json:"list"`
	}
	if err := json.Unmarshal(response, &data); err != nil {
		return nil, fmt.Errorf("极客盘响应无法解析")
	}
	if data.Msg != "success" {
		return nil, fmt.Errorf("极客盘搜索失败: %s", data.Msg)
	}
	results := []Result{}
	for _, item := range data.List {
		for _, link := range item.Links {
			if strings.EqualFold(link.Service, "quark") {
				if result, ok := quarkResult(item.Name, link.Link+passwordSuffix(link.Password), source.Name); ok {
					results = append(results, result)
				}
			}
		}
	}
	return results, nil
}

func (e *Engine) searchHunhepan(ctx context.Context, source Source, keyword string) ([]Result, error) {
	response, err := e.postJSON(ctx, source.Base, source.Base, map[string]any{
		"page": 1, "q": keyword, "user": "", "exact": false, "format": []string{}, "share_time": "", "size": 30, "type": "", "exclude_user": []string{},
		"adv_params": map[string]string{"wechat_pwd": "", "platform": "pc"},
	})
	if err != nil {
		return nil, err
	}
	var data struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			List []struct {
				Name     string `json:"disk_name"`
				Link     string `json:"link"`
				Password string `json:"disk_pass"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &data); err != nil {
		return nil, fmt.Errorf("混合盘响应无法解析")
	}
	if data.Code != 200 {
		return nil, fmt.Errorf("混合盘返回状态 %d", data.Code)
	}
	results := []Result{}
	for _, item := range data.Data.List {
		if result, ok := quarkResult(item.Name, item.Link+passwordSuffix(item.Password), source.Name); ok {
			results = append(results, result)
		}
	}
	return results, nil
}

func (e *Engine) postJSON(ctx context.Context, endpoint, referer string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("搜索请求无法编码")
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-timeAfterBackoff(attempt):
			case <-ctx.Done():
				return nil, fmt.Errorf("搜索请求已取消")
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("搜索地址无效")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120 Safari/537.36")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		res, requestErr := e.client.Do(req)
		if requestErr != nil {
			lastErr = requestErr
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, 3<<20))
		res.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d", res.StatusCode)
			continue
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("搜索来源 HTTP %d", res.StatusCode)
		}
		return data, nil
	}
	return nil, fmt.Errorf("搜索来源请求失败: %w", lastErr)
}

func passwordSuffix(code string) string {
	if code == "" {
		return ""
	}
	return " 提取码:" + code
}

func quarkResult(title, link, source string) (Result, bool) {
	links := ExtractLinks(link)
	if len(links) == 0 {
		return Result{}, false
	}
	title = strings.TrimSpace(stdhtml.UnescapeString(stripTags(title)))
	if title == "" {
		title = "夸克分享"
	}
	return Result{Title: title, URL: links[0], Source: source}, true
}

func (e *Engine) searchPanta(ctx context.Context, source Source, keyword string) ([]Result, error) {
	base := strings.TrimRight(source.Base, "/")
	page, err := e.get(ctx, base+"/search?keyword="+url.QueryEscape(keyword))
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("盘他搜索页无法解析")
	}
	type item struct{ title, id string }
	items := []item{}
	visit(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "div" || !hasClass(n, "topicItem") {
			return
		}
		visit(n, func(child *html.Node) {
			if child.Type != html.ElementNode || child.Data != "a" {
				return
			}
			href := attr(child, "href")
			if strings.Contains(href, "topicId=") {
				parsed, parseErr := url.Parse(href)
				if parseErr == nil && parsed.Query().Get("topicId") != "" {
					items = append(items, item{strings.TrimSpace(text(child)), parsed.Query().Get("topicId")})
				}
			}
		})
	})
	if len(items) == 0 {
		return []Result{}, nil
	}
	results := []Result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 3)
	failed := 0
	for _, item := range items[:min(12, len(items))] {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-semaphore }()
			threadURL := base + "/thread?topicId=" + url.QueryEscape(item.id)
			thread, fetchErr := e.getWithHeaders(ctx, threadURL, "text/html,application/xhtml+xml,*/*;q=0.8", base+"/index")
			mu.Lock()
			defer mu.Unlock()
			if fetchErr != nil {
				failed++
				return
			}
			for _, link := range ExtractLinks(thread) {
				results = append(results, Result{Title: item.title, URL: link, Source: source.Name})
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		return results, fmt.Errorf("%d 个盘他帖子读取失败，已保留其他结果", failed)
	}
	return results, nil
}

func (e *Engine) searchDuoduo(ctx context.Context, source Source, keyword string) ([]Result, error) {
	base := strings.TrimRight(source.Base, "/")
	page, err := e.get(ctx, base+"/index.php/vod/search/wd/"+url.PathEscape(keyword)+".html")
	if err != nil {
		return nil, err
	}
	return e.parseDetailSearchPage(ctx, source, base, page)
}

func (e *Engine) parseDetailSearchPage(ctx context.Context, source Source, base, page string) ([]Result, error) {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("%s 搜索页无法解析", source.Name)
	}
	items := []detailItem{}
	visit(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href := attr(n, "href")
		if strings.Contains(href, "/vod/detail/id/") || strings.Contains(href, "/book/") {
			items = append(items, detailItem{title: strings.TrimSpace(text(n)), url: resolveSameHost(base, href)})
		}
	})
	return e.fetchDetailItems(ctx, source, items)
}

func (e *Engine) searchXuexizhinan(ctx context.Context, source Source, keyword string) ([]Result, error) {
	base := strings.TrimRight(source.Base, "/")
	page, err := e.get(ctx, base+"/?post_type=book&s="+url.QueryEscape(keyword))
	if err != nil {
		return nil, err
	}
	return e.parseDetailSearchPage(ctx, source, base, page)
}

type detailItem struct{ title, url string }

func (e *Engine) fetchDetailItems(ctx context.Context, source Source, items []detailItem) ([]Result, error) {
	if len(items) > 12 {
		items = items[:12]
	}
	out := make([]Result, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	failed := 0
	for _, item := range items {
		item := item
		if item.title == "" || item.url == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			page, err := e.get(ctx, item.url)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				return
			}
			for _, link := range ExtractLinks(page) {
				out = append(out, Result{Title: item.title, URL: link, Source: source.Name})
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		return out, fmt.Errorf("%d 个详情页请求失败，已保留其余结果", failed)
	}
	return out, nil
}

func resolveSameHost(base, href string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return ""
	}
	reference, err := url.Parse(href)
	if err != nil {
		return ""
	}
	resolved := baseURL.ResolveReference(reference)
	if resolved.Host != baseURL.Host {
		return ""
	}
	return resolved.String()
}

func hasClass(n *html.Node, class string) bool {
	for _, value := range strings.Fields(attr(n, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func stripTags(value string) string {
	var out strings.Builder
	inside := false
	for _, r := range value {
		if r == '<' {
			inside = true
			continue
		}
		if r == '>' {
			inside = false
			out.WriteByte(' ')
			continue
		}
		if !inside {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (e *Engine) searchTelegram(ctx context.Context, source Source, keyword string) ([]Result, error) {
	channels := source.Channels
	if len(channels) == 0 {
		channels = panHubChannels()
	}
	base := strings.TrimRight(source.Base, "/")
	sem := make(chan struct{}, 8)
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := []Result{}
	var succeeded atomic.Int32
	for _, channel := range channels {
		channel := channel
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			page, err := e.get(ctx, base+"/"+url.PathEscape(channel))
			if err != nil || !strings.Contains(page, "tgme_widget_message") {
				if strings.HasPrefix(base, "https://t.me/s") {
					page, err = e.get(ctx, "https://r.jina.ai/https://t.me/s/"+url.PathEscape(channel))
				}
			}
			if err != nil {
				return
			}
			succeeded.Add(1)
			found := parseTelegramPage(page, channel, keyword, source.Name)
			mu.Lock()
			results = append(results, found...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return results, fmt.Errorf("Telegram 搜索已超时或取消，已保留部分结果")
	}
	if succeeded.Load() == 0 {
		return results, fmt.Errorf("Telegram 频道均无法读取（%d 个频道）", len(channels))
	}
	return results, nil
}

func parseTelegramPage(page, channel, keyword, source string) []Result {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil
	}
	results := []Result{}
	visit(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || !hasClass(n, "tgme_widget_message_wrap") {
			return
		}
		var textParts []string
		var content strings.Builder
		var appendMessage func(*html.Node, bool)
		appendMessage = func(child *html.Node, insideLink bool) {
			if child.Type == html.TextNode {
				if !insideLink {
					textParts = append(textParts, child.Data)
				}
				content.WriteString(child.Data)
				content.WriteByte(' ')
			}
			linkNode := child.Type == html.ElementNode && child.Data == "a"
			if linkNode && attr(child, "href") != "" {
				content.WriteByte(' ')
				content.WriteString(attr(child, "href"))
				content.WriteByte(' ')
			}
			for c := child.FirstChild; c != nil; c = c.NextSibling {
				appendMessage(c, insideLink || linkNode)
			}
		}
		appendMessage(n, false)
		message := content.String()
		if !matchesKeyword(message, keyword) {
			return
		}
		links := ExtractLinks(message)
		if len(links) == 0 {
			return
		}
		title := strings.TrimSpace(strings.SplitN(strings.Join(textParts, " "), "提取码", 2)[0])
		title = strings.Join(strings.Fields(title), " ")
		if title == "" {
			title = channel
		}
		for _, link := range links {
			results = append(results, Result{Title: title, URL: link, Source: source})
		}
	})
	if len(results) == 0 && matchesKeyword(page, keyword) {
		for _, link := range ExtractLinks(page) {
			results = append(results, Result{Title: keyword, URL: link, Source: source})
		}
	}
	return results
}

func matchesKeyword(text, keyword string) bool {
	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
	needle := normalize(keyword)
	if needle == "" {
		return false
	}
	if strings.Contains(normalize(text), needle) {
		return true
	}
	for _, part := range strings.FieldsFunc(keyword, func(r rune) bool { return strings.ContainsRune(" 的与和及、，,。:：/|", r) }) {
		part = normalize(part)
		if len([]rune(part)) >= 2 && strings.Contains(normalize(text), part) {
			return true
		}
	}
	return false
}

func panHubChannels() []string {
	return []string{"tgsearchers3", "share_aliyun", "Quark_Movies", "NewQuark", "alyp_4K_Movies", "yunpanqk", "BaiduCloudDisk", "tianyifc", "yunpanxunlei", "txtyzy", "peccxinpd", "gotopan", "xingqiump4", "PanjClub", "kkxlzy", "baicaoZY", "MCPH01", "bdwpzhpd", "ysxb48", "jdjdn1111", "yggpan", "MCPH086", "zaihuayun", "Q66Share", "NewAliPan", "ypquark", "Oscar_4Kmovies", "ucwpzy", "alyp_TV", "shareAliyun", "alyp_1", "dianyingshare", "XiangxiuNBB", "ydypzyfx", "kuakeyun", "ucquark", "xx123pan", "yingshifenxiang123", "zyfb123", "tyypzhpd", "tianyirigeng", "cloudtianyi", "hdhhd21", "Lsp115", "oneonefivewpfx", "Maidanglaocom", "qixingzhenren", "taoxgzy", "tgsearchers115", "Channel_Shares_115", "tyysypzypd", "vip115hot", "wp123zy", "yunpan139", "yunpan189", "yunpanuc", "yydf_hzl", "alyp_Animation", "alyp_JLP", "leoziyuan", "AliyunDrive_Share_Channel", "aliyunys", "Aliyun_4K_Movies", "yunpanpan", "Quark_Share_Channel", "quarkshare", "baiduyun", "iAliyun", "quanziyuanshe"}
}

func timeAfterBackoff(attempt int) <-chan time.Time {
	return time.After(time.Duration(1<<uint(attempt-1)) * 150 * time.Millisecond)
}

var _ = http.MethodGet
