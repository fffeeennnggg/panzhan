package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"panzhan/internal/quark"
	"panzhan/internal/search"
	"panzhan/internal/web"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func body(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("请求格式无效")
	}
	return nil
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", web.Handler())
	mux.HandleFunc("/api/", a.api)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'")
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/api/agent/v1/") {
			a.agentHandler(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "GET" {
				if !sameOrigin(r) && !a.validRequestToken(r) {
					writeJSON(w, 403, map[string]string{"error": "请求校验未通过，请刷新盘栈页面后重试"})
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 96<<10)
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	fail := func(err error) { writeJSON(w, 400, map[string]string{"error": err.Error()}) }
	ok := func(v any) { writeJSON(w, 200, v) }
	path := r.URL.Path
	if path == "/api/status" && r.Method == "GET" {
		ok(map[string]any{"configured": a.isConfigured(), "authenticated": a.authenticated(r), "version": "0.2.0", "request_token": a.requestToken})
		return
	}
	if (path == "/api/setup" || path == "/api/login") && r.Method == "POST" {
		if a.rateLimited() {
			writeJSON(w, 429, map[string]string{"error": "尝试过于频繁，请一分钟后再试"})
			return
		}
		var in struct {
			Password string `json:"password"`
			Token    string `json:"token"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		if path == "/api/setup" {
			passwordLength := utf8.RuneCountInString(in.Password)
			if passwordLength < 8 || passwordLength > 128 {
				fail(errors.New("管理密码需要 8–128 个字符"))
				return
			}
			token, err := os.ReadFile(filepath.Join(a.dataDir, "setup-token.txt"))
			if err != nil || !constantEqual(strings.TrimSpace(string(token)), strings.TrimSpace(in.Token)) {
				writeJSON(w, 403, map[string]string{"error": "安装令牌不正确，请读取 NAS 的 setup-token.txt"})
				return
			}
			err = a.change(func(st *State) error {
				if st.PasswordHash != "" {
					return errors.New("应用已经初始化")
				}
				st.Salt = randomID()
				st.PasswordHash = passwordHash(in.Password, st.Salt)
				return nil
			})
			if err != nil {
				fail(err)
				return
			}
			_ = os.Remove(filepath.Join(a.dataDir, "setup-token.txt"))
		} else {
			st := a.snapshot()
			if len(in.Password) > 512 || st.PasswordHash == "" || !constantEqual(passwordHash(in.Password, st.Salt), st.PasswordHash) {
				writeJSON(w, 401, map[string]string{"error": "密码不正确，可在飞牛应用中心的盘栈设置中重新设置管理密码"})
				return
			}
		}
		a.login(w, r)
		ok(map[string]bool{"ok": true})
		return
	}
	if !a.authenticated(r) {
		writeJSON(w, 401, map[string]string{"error": "请先登录"})
		return
	}
	if path == "/api/logout" && r.Method == "POST" {
		c, _ := r.Cookie("panzhan_session")
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "panzhan_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		ok(map[string]bool{"ok": true})
		return
	}
	switch {
	case path == "/api/agents" || strings.HasPrefix(path, "/api/agents/"):
		a.agentAdmin(w, r)
	case path == "/api/settings" && r.Method == "GET":
		settings := a.snapshot().Settings
		if settings.DownloadRoot == "" {
			settings.DownloadRoot = a.rootPath
		}
		ok(map[string]any{"settings": settings, "root": a.rootPath, "version": "0.2.0"})
	case path == "/api/settings" && r.Method == "POST":
		var in Settings
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		if in.SearchPriorityConcurrency == 0 {
			in.SearchPriorityConcurrency = 3
		}
		if in.SearchChannelConcurrency == 0 {
			in.SearchChannelConcurrency = 8
		}
		if in.Concurrency < 1 || in.Concurrency > 8 || in.ChunkMiB < 1 || in.ChunkMiB > 64 || in.SearchPriorityConcurrency < 1 || in.SearchPriorityConcurrency > 16 || in.SearchChannelConcurrency < 1 || in.SearchChannelConcurrency > 16 {
			fail(errors.New("单文件并发需为 1–8，分片需为 1–64 MiB，频道并发需为 1–16"))
			return
		}
		if in.DownloadRoot == "" {
			in.DownloadRoot = a.rootPath
		}
		downloadRoot, err := validateDownloadRoot(in.DownloadRoot, true)
		if err != nil {
			fail(err)
			return
		}
		in.DownloadRoot = downloadRoot
		d, err := a.directoryAt(downloadRoot, in.DefaultDir)
		if err != nil {
			fail(errors.New("默认目录不在已授权的位置内，或目录不存在"))
			return
		}
		d.Close()
		knownSources := map[string]bool{}
		for _, source := range a.search.Sources() {
			knownSources[source.ID] = true
		}
		for _, id := range in.Sources {
			if !knownSources[id] {
				fail(errors.New("未知搜索源"))
				return
			}
		}
		if err = a.change(func(s *State) error { s.Settings = in; return nil }); err != nil {
			fail(err)
			return
		}
		ok(map[string]bool{"ok": true})
	case path == "/api/account" && r.Method == "GET":
		ok(map[string]any{"configured": a.snapshot().CookieCipher != ""})
	case path == "/api/account" && r.Method == "POST":
		var in struct {
			Cookie string `json:"cookie"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		in.Cookie = strings.TrimSpace(in.Cookie)
		if strings.ContainsAny(in.Cookie, "\r\n") || len(in.Cookie) > 64000 {
			fail(errors.New("Cookie 格式无效"))
			return
		}
		encrypted := ""
		var err error
		if in.Cookie != "" {
			if !strings.Contains(in.Cookie, "=") {
				fail(errors.New("请输入夸克网页端 Cookie"))
				return
			}
			encrypted, err = a.seal(in.Cookie)
		}
		if err != nil {
			fail(err)
			return
		}
		if err = a.change(func(s *State) error { s.AccountRevision++; s.CookieCipher = encrypted; return nil }); err != nil {
			fail(err)
			return
		}
		ok(map[string]bool{"ok": true})
	case path == "/api/account/verify" && r.Method == "POST":
		c, err := a.provider()
		if err == nil {
			err = c.Verify(r.Context())
		}
		if err != nil {
			fail(err)
			return
		}
		ok(map[string]string{"message": "已通过当前账号的目录读取验证"})
	case path == "/api/sources" && r.Method == "GET":
		st := a.snapshot()
		list := []map[string]any{}
		for _, s := range a.search.Sources() {
			list = append(list, map[string]any{"id": s.ID, "name": s.Name, "base": s.Base, "enabled": slices.Contains(st.Settings.Sources, s.ID), "channels": s.Channels, "priority_channels": s.PriorityChannels, "priority": s.Priority, "needs_config": s.NeedsConfig})
		}
		ok(list)
	case path == "/api/search/check" && r.Method == "POST":
		var in struct {
			URLs []string `json:"urls"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		if len(in.URLs) == 0 || len(in.URLs) > 8 {
			fail(errors.New("每次验证 1–8 个分享"))
			return
		}
		checks := make([]quark.ShareCheck, len(in.URLs))
		var wg sync.WaitGroup
		for i, raw := range in.URLs {
			wg.Add(1)
			go func(i int, raw string) { defer wg.Done(); checks[i] = a.checkShare(r.Context(), raw) }(i, raw)
		}
		wg.Wait()
		warning := ""
		if r.Context().Err() == nil {
			if err := a.search.RememberChecks(checks); err != nil {
				warning = "验证已完成，但检查记录未能保存"
			}
		}
		ok(map[string]any{"checks": checks, "warning": warning})
	case (path == "/api/search" || path == "/api/search/local") && r.Method == "POST":
		var in struct {
			Query    string `json:"query"`
			SourceID string `json:"source_id"`
			Channel  string `json:"channel"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		in.Query = strings.TrimSpace(in.Query)
		if len(in.Query) == 0 || len(in.Query) > 600 {
			fail(errors.New("请输入不超过 200 字的关键词"))
			return
		}
		if in.Channel != "" && in.SourceID == "" {
			fail(errors.New("频道搜索需要指定来源"))
			return
		}
		enabled := a.snapshot().Settings.Sources
		if path == "/api/search/local" {
			if in.SourceID != "" || in.Channel != "" {
				fail(errors.New("本地查询不接受来源任务参数"))
				return
			}
			ok(a.search.Local(in.Query, enabled))
			return
		}
		if in.SourceID != "" {
			known := false
			for _, source := range a.search.Sources() {
				if source.ID == in.SourceID {
					known = true
					break
				}
			}
			if !known || !slices.Contains(enabled, in.SourceID) {
				fail(errors.New("搜索来源不存在或未启用"))
				return
			}
			enabled = []string{in.SourceID}
		}
		var report search.Report
		if in.SourceID != "" {
			report = a.search.SearchSource(r.Context(), in.Query, in.SourceID, in.Channel)
		} else {
			report = a.search.Search(r.Context(), in.Query, enabled)
		}
		ok(a.search.ApplyChecks(report))
	case path == "/api/preview" && r.Method == "POST":
		var in struct {
			URL  string `json:"url"`
			Code string `json:"code"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		s, files, err := a.preview(r.Context(), in.URL, in.Code)
		if err != nil {
			fail(err)
			return
		}
		for i := range files {
			files[i].Token = ""
		}
		warning := ""
		if err := a.search.MarkAccessible(s.URL); err != nil {
			warning = "已读取分享内容，但检查时间未能保存到本地资源库"
		}
		ok(map[string]any{"share": s, "files": files, "checked_at": time.Now().UTC().Format(time.RFC3339), "warning": warning})
	case path == "/api/tasks" && r.Method == "GET":
		st := a.snapshot()
		for i := range st.Jobs {
			st.Jobs[i].Original = nil
			st.Jobs[i].Share.Code = ""
		}
		ok(a.withTransferProgress(st.Jobs))
	case path == "/api/tasks" && r.Method == "POST":
		var in struct {
			URL          string `json:"url"`
			Title        string `json:"title"`
			Directory    string `json:"directory"`
			DownloadRoot string `json:"download_root"`
			Code         string `json:"code"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		if in.DownloadRoot == "" {
			in.DownloadRoot = a.snapshot().Settings.DownloadRoot
		}
		if in.DownloadRoot == "" {
			in.DownloadRoot = a.rootPath
		}
		var rootErr error
		in.DownloadRoot, rootErr = validateDownloadRoot(in.DownloadRoot, true)
		if rootErr != nil {
			fail(rootErr)
			return
		}
		job, err := a.createJobAt(r.Context(), in.URL, in.Title, in.Directory, in.Code, in.DownloadRoot)
		if err != nil {
			fail(err)
			return
		}
		ok(map[string]string{"id": job.ID})
	case strings.HasPrefix(path, "/api/tasks/") && strings.HasSuffix(path, "/diagnose") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/tasks/"), "/diagnose")
		var in downloadProbeOptions
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		result, err := a.diagnoseDownload(r.Context(), id, in)
		if err != nil {
			fail(err)
			return
		}
		ok(result)
	case strings.HasPrefix(path, "/api/tasks/") && r.Method == "POST":
		id := strings.TrimPrefix(path, "/api/tasks/")
		var in struct {
			Action    string `json:"action"`
			Directory string `json:"directory"`
		}
		if err := body(r, &in); err != nil {
			fail(err)
			return
		}
		if err := a.action(r.Context(), id, in.Action, in.Directory); err != nil {
			fail(err)
			return
		}
		ok(map[string]bool{"ok": true})
	case path == "/api/directories" && r.Method == "GET":
		rel := r.URL.Query().Get("path")
		rootPath := r.URL.Query().Get("root")
		if rootPath == "" {
			rootPath = a.snapshot().Settings.DownloadRoot
		}
		if rootPath == "" {
			rootPath = a.rootPath
		}
		rootPath, err := validateDownloadRoot(rootPath, false)
		if err != nil {
			fail(errors.New("无法访问此位置；请先在飞牛应用设置中为盘栈授权该目录"))
			return
		}
		root, err := a.directoryAt(rootPath, rel)
		if err != nil {
			fail(errors.New("目录不在授权范围内或不存在"))
			return
		}
		defer root.Close()
		f, err := root.Open(".")
		if err != nil {
			fail(err)
			return
		}
		defer f.Close()
		entries, err := f.ReadDir(0)
		if err != nil {
			fail(errors.New("目录无法读取"))
			return
		}
		dirs := []string{}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				dirs = append(dirs, e.Name())
			}
		}
		ok(map[string]any{"path": rel, "directories": dirs, "root": rootPath})
	default:
		writeJSON(w, 404, map[string]string{"error": "接口不存在"})
	}
}
