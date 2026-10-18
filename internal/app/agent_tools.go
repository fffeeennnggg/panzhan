// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"panzhan/internal/search"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type agentTaskView struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Status    string        `json:"status"`
	Directory string        `json:"directory"`
	Context   string        `json:"context,omitempty"`
	Error     string        `json:"error,omitempty"`
	Created   string        `json:"created_at"`
	Progress  *TaskProgress `json:"progress,omitempty"`
	Files     int           `json:"file_count"`
	Completed int           `json:"completed_files"`
	Size      int64         `json:"total_bytes"`
}

func viewAgentTask(j Job) agentTaskView {
	v := agentTaskView{ID: j.ID, Title: j.Title, Status: j.Status, Directory: j.Directory, Context: j.AgentContext, Error: j.Error, Created: j.Created, Progress: j.Progress, Files: len(j.Files)}
	for _, f := range j.Files {
		v.Size += f.Size
		if f.Done {
			v.Completed++
		}
	}
	return v
}
func decodeAgentArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("工具参数格式无效或包含未支持的字段")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("工具参数必须是单个 JSON 对象")
	}
	return nil
}
func agentToolScope(name string) string {
	switch name {
	case "panzhan_search", "panzhan_preview", "panzhan_sources":
		return "resources:read"
	case "panzhan_download":
		return "downloads:create"
	case "panzhan_tasks":
		return "tasks:read"
	case "panzhan_events", "panzhan_ack_event":
		return "events:read"
	}
	return ""
}
func (a *App) callAgentTool(ctx context.Context, g AgentGrant, name string, args json.RawMessage) (any, error) {
	scope := agentToolScope(name)
	a.mu.Lock()
	allowed := scope != "" && liveAgentScope(&a.state, g.ID, scope)
	a.mu.Unlock()
	if !allowed {
		return nil, errors.New("工具不存在或此 Agent 没有调用权限")
	}
	switch name {
	case "panzhan_sources":
		var in struct{}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		enabled := a.snapshot().Settings.Sources
		out := []map[string]string{}
		for _, s := range a.search.Sources() {
			if slices.Contains(enabled, s.ID) {
				out = append(out, map[string]string{"id": s.ID, "name": s.Name})
			}
		}
		return map[string]any{"sources": out}, nil
	case "panzhan_search":
		var in struct {
			Query     string `json:"query"`
			Limit     int    `json:"limit"`
			SourceID  string `json:"source_id"`
			LocalOnly bool   `json:"local_only"`
		}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		in.Query = strings.TrimSpace(in.Query)
		if utf8.RuneCountInString(in.Query) < 1 || utf8.RuneCountInString(in.Query) > 200 {
			return nil, errors.New("关键词需为 1–200 个字符")
		}
		if in.Limit == 0 {
			in.Limit = 5
		}
		if in.Limit < 1 || in.Limit > 20 {
			return nil, errors.New("候选数量需为 1–20")
		}
		enabled := a.snapshot().Settings.Sources
		if in.SourceID != "" {
			if !slices.Contains(enabled, in.SourceID) {
				return nil, errors.New("来源未启用")
			}
			enabled = []string{in.SourceID}
		}
		bounded, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		report := a.search.Local(in.Query, enabled)
		if !in.LocalOnly {
			remoteCtx, stop := context.WithTimeout(bounded, 17*time.Second)
			remote := a.search.Search(remoteCtx, in.Query, enabled)
			stop()
			report.Results = append(report.Results, remote.Results...)
			report.Sources = remote.Sources
			if remote.Warning != "" {
				report.Warning = remote.Warning
			}
		}
		report = a.search.ApplyChecks(report)
		seen := map[string]bool{}
		results := []search.Result{}
		for _, r := range report.Results {
			key := r.ID
			if key == "" {
				key = r.URL
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, r)
		}
		if len(results) > 20 {
			results = results[:20]
		}
		var wg sync.WaitGroup
		slots := make(chan struct{}, 3)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
				case <-bounded.Done():
					return
				}
				defer func() { <-slots }()
				if bounded.Err() != nil {
					return
				}
				check := a.checkShare(bounded, results[i].URL)
				results[i].Availability = check.Availability
				results[i].CheckedAt = check.CheckedAt
				results[i].CheckMessage = check.Message
				results[i].Content = check.Content
				results[i].FileCount = check.FileCount
			}(i)
		}
		wg.Wait()
		filtered := results[:0]
		for _, r := range results {
			if r.Availability != "invalid" {
				filtered = append(filtered, r)
			}
		}
		results = filtered
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Availability == "accessible" && results[j].Availability != "accessible"
		})
		if len(results) > in.Limit {
			results = results[:in.Limit]
		}
		failures := 0
		for _, s := range report.Sources {
			if s.Error != "" {
				failures++
			}
		}
		return map[string]any{"results": results, "source_count": len(report.Sources), "source_errors": failures, "warning": report.Warning, "next": "请用户选择候选，再调用 panzhan_preview；仅在用户明确确认后调用 panzhan_download"}, nil
	case "panzhan_preview":
		var in struct {
			URL     string `json:"url"`
			Code    string `json:"code"`
			Title   string `json:"title"`
			Context string `json:"context"`
		}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		in.Context = strings.TrimSpace(in.Context)
		if len(in.Context) == 0 || len(in.Context) > 256 || len(in.Title) > 600 {
			return nil, errors.New("需要 1–256 字节的原会话标识，标题不能超过 600 字节")
		}
		s, files, err := a.preview(ctx, in.URL, in.Code)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errors.New("分享目录为空")
		}
		if in.Title == "" {
			in.Title = files[0].Name
		}
		in.Title = safeTitle(in.Title)
		choice := AgentChoice{ID: randomID(), GrantID: g.ID, URL: in.URL, Code: s.Code, Title: in.Title, Context: in.Context, Expires: time.Now().UTC().Add(15 * time.Minute)}
		err = a.change(func(st *State) error {
			if !liveAgentScope(st, g.ID, "resources:read") {
				return errors.New("Agent 授权已失效")
			}
			kept := st.AgentChoices[:0]
			count := 0
			for _, c := range st.AgentChoices {
				if time.Now().Before(c.Expires) {
					if c.GrantID == g.ID {
						count++
					}
					kept = append(kept, c)
				}
			}
			if count >= 64 {
				return errors.New("待确认候选过多，请稍后重试")
			}
			st.AgentChoices = append(kept, choice)
			return nil
		})
		if err != nil {
			return nil, err
		}
		items := []map[string]any{}
		var size int64
		for i, f := range files {
			if f.IsFile {
				size += f.Size
			}
			if i < 200 {
				items = append(items, map[string]any{"name": f.Name, "size": f.Size, "is_file": f.IsFile})
			}
		}
		return map[string]any{"choice_id": choice.ID, "title": choice.Title, "files": items, "item_count": len(files), "listed_file_bytes": size, "size_note": "仅包含当前目录文件，文件夹大小未递归统计", "expires_at": choice.Expires, "download_directory": g.Directory, "requires_confirmation": true}, nil
	case "panzhan_download":
		var in struct {
			ChoiceID  string `json:"choice_id"`
			RequestID string `json:"request_id"`
			Confirmed bool   `json:"user_confirmed"`
		}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		if !in.Confirmed {
			return nil, errors.New("请先取得用户明确的资源选择与下载确认")
		}
		if len(in.RequestID) < 1 || len(in.RequestID) > 128 {
			return nil, errors.New("需要 1–128 字符的唯一 request_id 防止重复下载")
		}
		a.agentMu.Lock()
		defer a.agentMu.Unlock()
		st := a.snapshot()
		for _, job := range st.Jobs {
			if job.AgentID == g.ID && job.AgentRequest == in.RequestID {
				if job.AgentChoice != in.ChoiceID {
					return nil, errors.New("request_id 已用于其他资源")
				}
				return map[string]any{"task": viewAgentTask(job), "duplicate": true}, nil
			}
		}
		var choice AgentChoice
		for _, c := range st.AgentChoices {
			if c.ID == in.ChoiceID && c.GrantID == g.ID {
				choice = c
				break
			}
		}
		if choice.ID == "" || !time.Now().Before(choice.Expires) {
			return nil, errors.New("资源选择不存在或已过期，请重新预览")
		}
		if choice.TaskID != "" {
			for _, job := range st.Jobs {
				if job.ID == choice.TaskID {
					return map[string]any{"task": viewAgentTask(job), "duplicate": true}, nil
				}
			}
			return nil, errors.New("此资源选择已使用")
		}
		job, err := a.createAgentJob(ctx, choice.URL, choice.Title, g.Directory, choice.Code, g.Root, agentJobMeta{GrantID: g.ID, Context: choice.Context, RequestID: in.RequestID, ChoiceID: choice.ID})
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": viewAgentTask(job), "duplicate": false}, nil
	case "panzhan_tasks":
		var in struct {
			TaskID  string `json:"task_id"`
			Context string `json:"context"`
		}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		jobs := a.withTransferProgress(a.snapshot().Jobs)
		out := []agentTaskView{}
		for _, j := range jobs {
			if j.AgentID == g.ID && (in.TaskID == "" || j.ID == in.TaskID) && (in.Context == "" || j.AgentContext == in.Context) {
				out = append(out, viewAgentTask(j))
				if len(out) >= 50 {
					break
				}
			}
		}
		if in.TaskID != "" && len(out) == 0 {
			return nil, errors.New("任务不存在或不属于此授权")
		}
		return map[string]any{"tasks": out}, nil
	case "panzhan_events":
		var in struct {
			Wait int `json:"wait_seconds"`
		}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		events, err := a.agentEvents(ctx, g, in.Wait)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": events}, nil
	case "panzhan_ack_event":
		var in struct {
			ID uint64 `json:"event_id"`
		}
		if err := decodeAgentArgs(args, &in); err != nil {
			return nil, err
		}
		if err := a.ackAgentEvent(g, in.ID); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	}
	return nil, fmt.Errorf("不支持的工具")
}
