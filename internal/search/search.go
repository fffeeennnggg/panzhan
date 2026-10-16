// SPDX-License-Identifier: AGPL-3.0-or-later
// Source routes/selectors and Quark extraction adapted from PanSou (MIT).
package search

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"panzhan/internal/quark"
)

type Source struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Base             string   `json:"base"`
	Priority         int      `json:"priority,omitempty"`
	NeedsConfig      bool     `json:"needs_config,omitempty"`
	SearchPath       string   `json:"-"`
	Channels         []string `json:"-"`
	PriorityChannels []string `json:"-"`
}
type Result struct {
	Title        string   `json:"title"`
	URL          string   `json:"url"`
	Source       string   `json:"source"`
	ID           string   `json:"id"`
	Provider     string   `json:"provider"`
	Origins      []Origin `json:"origins"`
	DiscoveredAt string   `json:"discovered_at,omitempty"`
	LastSeenAt   string   `json:"last_seen_at,omitempty"`
	PublishedAt  string   `json:"published_at,omitempty"`
	CheckedAt    string   `json:"checked_at,omitempty"`
	Availability string   `json:"availability"`
	Local        bool     `json:"local,omitempty"`
	Cached       bool     `json:"cached,omitempty"`
}
type Status struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Error         string `json:"error,omitempty"`
	Count         int    `json:"count"`
	ChannelCount  int    `json:"channel_count,omitempty"`
	ChannelErrors int    `json:"channel_errors,omitempty"`
	Checked       string `json:"checked"`
	Cached        bool   `json:"cached,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	RetryAt       string `json:"retry_at,omitempty"`
}
type Report struct {
	Results []Result `json:"results"`
	Sources []Status `json:"sources"`
	Warning string   `json:"warning,omitempty"`
}
type Engine struct {
	sources        []Source
	panSouPlugins  map[string]SourcePlugin
	client         *http.Client
	cacheMu        sync.Mutex
	cache          map[string]*list.Element
	lru            *list.List
	catalog        *Catalog
	catalogWarning string
	healthMu       sync.Mutex
	health         map[string]sourceHealth
	now            func() time.Time
}

type cachedReport struct {
	key     string
	report  Report
	expires time.Time
}

const (
	searchCacheTTL   = 5 * time.Minute
	searchCacheLimit = 128
)

func New() *Engine {
	sources := []Source{
		{ID: "pansearch", Name: "PanSearch", Base: "https://www.pansearch.me", SearchPath: "/search?keyword=%s"},
		{ID: "qupansou", Name: "夸克搜", Base: "https://v.funletu.com"},
		{ID: "panta", Name: "盘他", Base: "https://www.91panta.cn"},
		{ID: "hunhepan", Name: "混合盘", Base: "https://hunhepan.com/open/search/disk"},
		{ID: "jikepan", Name: "极客盘", Base: "https://api.jikepan.xyz"},
		{ID: "duoduo", Name: "多多资源", Base: "https://tv.yydsys.top"},
		{ID: "xuexizhinan", Name: "学习指南", Base: "https://xuexizhinan.com"},
		{ID: "telegram", Name: "Telegram 公开频道", Base: "https://t.me/s", Channels: panHubChannels(), PriorityChannels: []string{"tgsearchers3", "share_aliyun", "Quark_Movies", "NewQuark", "alyp_4K_Movies"}},
	}
	plugins := registeredPanSouPlugins()
	for _, source := range plugins {
		sources = append(sources, source.Source)
	}
	e := NewWithSources(sources)
	e.panSouPlugins = map[string]SourcePlugin{}
	for _, source := range plugins {
		e.panSouPlugins[source.Source.ID] = source.plugin
	}
	return e
}
func NewWithSources(s []Source) *Engine {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = platformSearchProxy
	return &Engine{sources: s, cache: map[string]*list.Element{}, lru: list.New(), health: map[string]sourceHealth{}, now: time.Now, client: &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 4 || r.URL.Host != via[0].URL.Host {
			return fmt.Errorf("来源跳转到其他站点，已停止")
		}
		return nil
	}}}
}
func (e *Engine) Sources() []Source { return append([]Source{}, e.sources...) }

var linkRE = regexp.MustCompile(`https?://pan\.quark\.cn/s/[A-Za-z0-9]+(?:\?[^\s<>"'，。]*)?(?:#[^\s<>"'，。]*)?`)
var buildIDRE = regexp.MustCompile(`(?i)["']?buildId["']?\s*:\s*["']([^"']+)["']`)

const (
	panSearchPageSize = 10
	panSearchMaxPages = 5
	sourceTimeout     = 12 * time.Second
	telegramTimeout   = 75 * time.Second
)

func ExtractLinks(text string) []string {
	text = stdhtml.UnescapeString(text)
	out := []string{}
	seen := map[string]bool{}
	matches := linkRE.FindAllStringIndex(text, -1)
	for i, loc := range matches {
		raw := text[loc[0]:loc[1]]
		s, err := quark.ParseShare(raw)
		if err != nil {
			continue
		}
		if s.Code == "" {
			end := min(len(text), loc[1]+512)
			if i+1 < len(matches) {
				end = min(end, matches[i+1][0])
			}
			if ss, er := quark.ParseShare(raw + " " + text[loc[1]:end]); er == nil {
				s.Code = ss.Code
			}
			if s.Code != "" {
				u, _ := url.Parse(raw)
				q := u.Query()
				q.Set("pwd", s.Code)
				u.RawQuery = q.Encode()
				raw = u.String()
			}
		}
		if !seen[raw] {
			seen[raw] = true
			out = append(out, raw)
		}
	}
	return out
}
func (e *Engine) get(ctx context.Context, u string) (string, error) {
	return e.getWithHeaders(ctx, u, "text/html,application/json;q=0.9,*/*;q=0.8", "")
}

func (e *Engine) getWithHeaders(ctx context.Context, u, accept, referer string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<(attempt-1)) * 150 * time.Millisecond
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		r, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return "", fmt.Errorf("来源地址无效: %w", err)
		}
		r.Header.Set("User-Agent", quark.UserAgent)
		r.Header.Set("Accept", accept)
		if referer != "" {
			r.Header.Set("Referer", referer)
		}
		res, err := e.client.Do(r)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return "", fmt.Errorf("来源请求超时或已取消")
			}
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(res.Body, 3<<20))
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
			return "", fmt.Errorf("来源 HTTP %d", res.StatusCode)
		}
		return string(b), nil
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("来源请求超时或已取消")
	}
	return "", fmt.Errorf("来源请求失败: %w", lastErr)
}
func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}
func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
	}
	return b.String()
}
func visit(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		visit(c, f)
	}
}
func (e *Engine) source(ctx context.Context, s Source, q string) ([]Result, error) {
	if plugin, ok := e.panSouPlugins[s.ID]; ok {
		return plugin.Search(ctx, q)
	}
	switch s.ID {
	case "qupansou":
		return e.searchQupansou(ctx, s, q)
	case "jikepan":
		return e.searchJikepan(ctx, s, q)
	case "hunhepan":
		return e.searchHunhepan(ctx, s, q)
	case "panta":
		return e.searchPanta(ctx, s, q)
	case "duoduo":
		return e.searchDuoduo(ctx, s, q)
	case "xuexizhinan":
		return e.searchXuexizhinan(ctx, s, q)
	case "telegram":
		return e.searchTelegram(ctx, s, q)
	}
	path := s.SearchPath
	if path == "" {
		if s.ID == "pansearch" {
			path = "/search?keyword=%s"
		} else {
			path = "/index.php/vod/search/wd/%s.html"
		}
	}
	target := s.Base
	if strings.Contains(path, "%s") {
		target += fmt.Sprintf(path, url.QueryEscape(q))
	} else {
		target += path
	}
	page, err := e.get(ctx, target)
	if err != nil {
		return nil, err
	}
	if s.ID == "pansearch" {
		return e.panSearch(ctx, s, target, page)
	}
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("无法解析来源页面")
	}
	type item struct{ title, href string }
	items := []item{}
	seen := map[string]bool{}
	visit(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href := attr(n, "href")
		if !strings.Contains(href, "/vod/detail/id/") || seen[href] {
			return
		}
		title := strings.TrimSpace(text(n))
		if title == "" {
			title = attr(n, "title")
		}
		if title == "" {
			return
		}
		seen[href] = true
		items = append(items, item{title, href})
	})
	if len(items) == 0 {
		if strings.Contains(page, "没有找到") || strings.Contains(page, "暂无数据") || strings.Contains(page, "未找到") {
			return []Result{}, nil
		}
		return nil, fmt.Errorf("未识别来源页面结构，可能需要更新解析器")
	}
	if len(items) > 12 {
		items = items[:12]
	}
	base, _ := url.Parse(s.Base)
	out := []Result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	failed := 0
	for _, it := range items {
		wg.Add(1)
		go func(it item) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			ref, er := url.Parse(it.href)
			if er != nil {
				return
			}
			u := base.ResolveReference(ref)
			if u.Host != base.Host {
				return
			}
			body, er := e.get(ctx, u.String())
			mu.Lock()
			defer mu.Unlock()
			if er != nil {
				failed++
				return
			}
			for _, link := range ExtractLinks(body) {
				out = append(out, Result{Title: it.title, URL: link, Source: s.Name})
			}
		}(it)
	}
	wg.Wait()
	if failed > 0 {
		return out, fmt.Errorf("%d 个详情页请求失败，已保留其他结果", failed)
	}
	return out, nil
}

func (e *Engine) panSearch(ctx context.Context, source Source, pageURL, page string) ([]Result, error) {
	build := buildIDRE.FindStringSubmatch(page)
	if len(build) < 2 {
		return parsePanSearch(page, source.Name)
	}
	base, err := url.Parse(source.Base)
	if err != nil {
		return nil, fmt.Errorf("PanSearch 来源地址无效")
	}
	fetch := func(buildID string, offset int) ([]string, int, error) {
		apiURL := *base
		apiURL.Path = "/_next/data/" + url.PathEscape(buildID) + "/search.json"
		query := apiURL.Query()
		query.Set("keyword", queryFromSearchURL(pageURL))
		query.Set("offset", fmt.Sprint(offset))
		apiURL.RawQuery = query.Encode()
		body, fetchErr := e.getWithHeaders(ctx, apiURL.String(), "application/json, text/plain, */*", pageURL)
		if fetchErr != nil {
			return nil, 0, fetchErr
		}
		var response struct {
			PageProps struct {
				Data struct {
					Total int `json:"total"`
					Items []struct {
						Content string `json:"content"`
					} `json:"data"`
				} `json:"data"`
			} `json:"pageProps"`
		}
		if err := json.Unmarshal([]byte(body), &response); err != nil || response.PageProps.Data.Items == nil {
			return nil, 0, fmt.Errorf("PanSearch JSON 响应结构无法识别")
		}
		contents := make([]string, 0, len(response.PageProps.Data.Items))
		for _, item := range response.PageProps.Data.Items {
			contents = append(contents, item.Content)
		}
		return contents, response.PageProps.Data.Total, nil
	}

	contents, total, err := fetch(build[1], 0)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		freshPage, pageErr := e.get(ctx, pageURL)
		if pageErr != nil {
			return nil, pageErr
		}
		if fresh := buildIDRE.FindStringSubmatch(freshPage); len(fresh) > 1 {
			page = freshPage
			build = fresh
			contents, total, err = fetch(build[1], 0)
		}
	}
	if err != nil {
		// Keep the server-rendered results as a fallback when its JSON endpoint changes.
		if results, parseErr := parsePanSearch(page, source.Name); parseErr == nil && len(results) > 0 {
			return results, fmt.Errorf("PanSearch API 不可用，已回退到首页结果: %w", err)
		}
		return nil, err
	}

	maxPages := min((total+panSearchPageSize-1)/panSearchPageSize, panSearchMaxPages)
	type pageResult struct {
		offset   int
		contents []string
		err      error
	}
	pageResults := make(chan pageResult, maxPages)
	var wg sync.WaitGroup
	for pageNo := 1; pageNo < maxPages; pageNo++ {
		offset := pageNo * panSearchPageSize
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, _, fetchErr := fetch(build[1], offset)
			pageResults <- pageResult{offset: offset, contents: items, err: fetchErr}
		}()
	}
	wg.Wait()
	close(pageResults)
	pages := []pageResult{}
	for result := range pageResults {
		pages = append(pages, result)
	}
	pageErrors := 0
	for i := 0; i < len(pages); i++ {
		for j := i + 1; j < len(pages); j++ {
			if pages[j].offset < pages[i].offset {
				pages[i], pages[j] = pages[j], pages[i]
			}
		}
	}
	for _, result := range pages {
		contents = append(contents, result.contents...)
		if result.err != nil {
			pageErrors++
		}
	}
	results := parsePanSearchContents(contents, source.Name)
	if pageErrors > 0 {
		return results, fmt.Errorf("PanSearch 有 %d 页请求失败，已保留其他结果", pageErrors)
	}
	return results, nil
}

func queryFromSearchURL(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("keyword")
}

// PanSearch publishes its initial results as JSON in the public search HTML.
// Parse that payload directly, without a separate service or a build-ID API.
var nextDataPattern = regexp.MustCompile(`(?is)<script\b[^>]*\bid=["']__NEXT_DATA__["'][^>]*>(.*?)</script\s*>`)

func parsePanSearch(page string, source string) ([]Result, error) {
	// Real result summaries can truncate markup inside a quoted attribute. Isolate
	// only the known JSON script so malformed surrounding HTML cannot swallow it.
	match := nextDataPattern.FindStringSubmatch(page)
	if len(match) < 2 {
		return nil, fmt.Errorf("未识别来源页面结构，可能需要更新解析器")
	}
	payload := match[1]
	var data struct {
		Props struct {
			PageProps struct {
				Data *struct {
					Items []struct {
						Content string `json:"content"`
					} `json:"data"`
				} `json:"data"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if json.Unmarshal([]byte(payload), &data) != nil || data.Props.PageProps.Data == nil {
		return nil, fmt.Errorf("未识别来源页面结构，可能需要更新解析器")
	}
	contents := make([]string, 0, len(data.Props.PageProps.Data.Items))
	for _, item := range data.Props.PageProps.Data.Items {
		contents = append(contents, item.Content)
	}
	return parsePanSearchContents(contents, source), nil
}

func parsePanSearchContents(contents []string, source string) []Result {
	out := []Result{}
	for _, itemContent := range contents {
		fragment, err := html.Parse(strings.NewReader(itemContent))
		if err != nil {
			continue
		}
		var plain strings.Builder
		var flatten func(*html.Node)
		flatten = func(n *html.Node) {
			if n.Type == html.TextNode {
				plain.WriteString(n.Data)
				return
			}
			if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
				return
			}
			if n.Type == html.ElementNode && (n.Data == "br" || n.Data == "p" || n.Data == "div") {
				plain.WriteByte('\n')
			}
			if n.Type == html.ElementNode && n.Data == "a" && linkRE.MatchString(attr(n, "href")) {
				plain.WriteString(attr(n, "href"))
				plain.WriteByte(' ')
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				flatten(c)
			}
		}
		flatten(fragment)
		content := stdhtml.UnescapeString(plain.String())
		matches := linkRE.FindAllStringIndex(content, -1)
		for i, loc := range matches {
			end := len(content)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			links := ExtractLinks(content[loc[0]:end])
			if len(links) == 0 {
				continue
			}
			before := content[:loc[0]]
			lines := strings.Split(before, "\n")
			title := strings.TrimSpace(lines[len(lines)-1])
			if title == "" || title == "链接：" || title == "链接:" || title == "夸克：" || title == "夸克:" {
				for n := len(lines) - 2; n >= 0; n-- {
					candidate := strings.TrimSpace(lines[n])
					if candidate == "" {
						continue
					}
					if !linkRE.MatchString(candidate) && !strings.Contains(candidate, "提取码") {
						title = candidate
					}
					break
				}
			}
			title = strings.Join(strings.Fields(title), " ")
			if title == "" {
				title = "夸克分享"
			}
			if chars := []rune(title); len(chars) > 160 {
				title = string(chars[:160]) + "…"
			}
			for _, link := range links {
				out = append(out, Result{Title: title, URL: link, Source: source})
			}
		}
	}
	return out
}
func (e *Engine) Search(ctx context.Context, q string, enabled []string) Report {
	on := map[string]bool{}
	for _, id := range enabled {
		on[id] = true
	}
	selected := make([]Source, 0, len(enabled))
	for _, source := range e.sources {
		if on[source.ID] {
			selected = append(selected, source)
		}
	}
	return e.searchSources(ctx, q, selected, searchCacheKey(q, enabled))
}

func (e *Engine) SearchSource(ctx context.Context, q, sourceID, channel string) Report {
	for _, source := range e.sources {
		if source.ID != sourceID {
			continue
		}
		cacheID := sourceID
		if channel != "" {
			if source.ID != "telegram" || !slices.Contains(source.Channels, channel) {
				return Report{Results: []Result{}, Sources: []Status{{ID: source.ID, Name: source.Name, Error: "Telegram 频道不存在"}}}
			}
			source.Channels = []string{channel}
			cacheID += ":" + channel
		}
		return e.searchSources(ctx, q, []Source{source}, searchCacheKey(q, []string{cacheID}))
	}
	return Report{Results: []Result{}, Sources: []Status{}}
}

func (e *Engine) searchSources(ctx context.Context, q string, sources []Source, _ string) Report {
	report := Report{Results: []Result{}, Sources: []Status{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, source := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			part := e.runSource(ctx, s, q)
			mu.Lock()
			report.Results = append(report.Results, part.Results...)
			report.Sources = append(report.Sources, part.Sources...)
			if part.Warning != "" {
				report.Warning = part.Warning
			}
			mu.Unlock()
		}(source)
	}
	wg.Wait()
	report.Results = mergeResults(report.Results)
	sortResultsByPriority(report.Results, sources)
	sort.Slice(report.Sources, func(i, j int) bool { return report.Sources[i].ID < report.Sources[j].ID })
	return report
}

func sortResultsByPriority(results []Result, sources []Source) {
	type rank struct{ priority, order int }
	ranks := make(map[string]rank, len(sources))
	for index, source := range sources {
		ranks[source.ID] = rank{priority: source.Priority, order: index}
	}
	getRank := func(result Result) rank {
		best := rank{order: len(sources)}
		for _, origin := range result.Origins {
			if current, ok := ranks[origin.ID]; ok && (current.priority > best.priority || current.priority == best.priority && current.order < best.order) {
				best = current
			}
		}
		return best
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := getRank(results[i]), getRank(results[j])
		if left.priority != right.priority {
			return left.priority > right.priority
		}
		return left.order < right.order
	})
}

func searchCacheKey(q string, enabled []string) string {
	ids := append([]string(nil), enabled...)
	sort.Strings(ids)
	return strings.ToLower(strings.TrimSpace(q)) + "\x00" + strings.Join(ids, ",")
}

func (e *Engine) cached(key string) (Report, bool) {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	element := e.cache[key]
	if element == nil {
		return Report{}, false
	}
	item := element.Value.(*cachedReport)
	if time.Now().After(item.expires) {
		delete(e.cache, key)
		e.lru.Remove(element)
		return Report{}, false
	}
	e.lru.MoveToFront(element)
	return cloneReport(item.report), true
}

func (e *Engine) store(key string, report Report) {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if element := e.cache[key]; element != nil {
		e.lru.Remove(element)
	}
	e.cache[key] = e.lru.PushFront(&cachedReport{key: key, report: cloneReport(report), expires: time.Now().Add(searchCacheTTL)})
	for e.lru.Len() > searchCacheLimit {
		last := e.lru.Back()
		item := last.Value.(*cachedReport)
		delete(e.cache, item.key)
		e.lru.Remove(last)
	}
}

func cloneReport(report Report) Report {
	copy := Report{Results: append([]Result{}, report.Results...), Sources: append([]Status{}, report.Sources...), Warning: report.Warning}
	for i := range copy.Results {
		copy.Results[i].Origins = append([]Origin{}, copy.Results[i].Origins...)
	}
	return copy
}
