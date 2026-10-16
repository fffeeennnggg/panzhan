// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"context"
	"strings"
	"time"
)

type sourceHealth struct {
	Failures int
	Until    time.Time
	Probing  bool
}

func (e *Engine) allowSource(key string) (bool, time.Time) {
	e.healthMu.Lock()
	defer e.healthMu.Unlock()
	h := e.health[key]
	if h.Failures < 3 {
		return true, time.Time{}
	}
	if e.now().Before(h.Until) || h.Probing {
		return false, h.Until
	}
	h.Probing = true
	e.health[key] = h
	return true, time.Time{}
}

func (e *Engine) recordSource(key string, failed, cancelled bool) {
	e.healthMu.Lock()
	defer e.healthMu.Unlock()
	h := e.health[key]
	h.Probing = false
	if cancelled {
		e.health[key] = h
		return
	}
	if !failed {
		delete(e.health, key)
		return
	}
	h.Failures++
	if h.Failures >= 3 {
		h.Until = e.now().Add(time.Minute * time.Duration(1<<min(h.Failures-3, 3)))
	}
	e.health[key] = h
}

func (e *Engine) runSource(ctx context.Context, s Source, q string) Report {
	key := s.ID
	if s.ID == "telegram" {
		key += ":" + strings.Join(s.Channels, ",")
	}
	cacheKey := searchCacheKey(q, []string{key})
	if cached, ok := e.cached(cacheKey); ok {
		for i := range cached.Results {
			cached.Results[i].Cached = true
		}
		for i := range cached.Sources {
			cached.Sources[i].Cached = true
		}
		return cached
	}
	status := Status{ID: s.ID, Name: s.Name, Checked: e.now().UTC().Format(time.RFC3339)}
	if s.ID == "telegram" {
		status.ChannelCount = len(s.Channels)
	}
	report := Report{Results: []Result{}, Sources: []Status{}}
	if allowed, retry := e.allowSource(key); !allowed {
		status.Error = "来源连续失败，暂缓请求；已有资源仍可查看"
		status.RetryAt = retry.UTC().Format(time.RFC3339)
		if s.ID == "telegram" {
			status.ChannelErrors = status.ChannelCount
		}
		report.Sources = append(report.Sources, status)
		return report
	}
	timeout := sourceTimeout
	if s.ID == "telegram" && len(s.Channels) != 1 {
		timeout = telegramTimeout
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	results, err := e.source(child, s, q)
	status.DurationMS = time.Since(start).Milliseconds()
	e.recordSource(key, err != nil, ctx.Err() != nil)
	if err != nil {
		status.Error = err.Error()
		if s.ID == "telegram" {
			status.ChannelErrors = 1
		}
	}
	if e.catalog != nil {
		var saveErr error
		results, saveErr = e.catalog.Remember(results, s)
		if saveErr != nil {
			report.Warning = "结果已返回，但本地资源库保存失败；请检查应用数据目录空间和权限"
		}
	} else {
		report.Warning = e.catalogWarning
		normalized := []Result{}
		for _, r := range results {
			if r, ok := normalizeResult(r, s, e.now()); ok {
				normalized = append(normalized, r)
			}
		}
		results = mergeResults(normalized)
	}
	status.Count = len(results)
	report.Results = results
	report.Sources = append(report.Sources, status)
	if err == nil && ctx.Err() == nil && report.Warning == "" {
		e.store(cacheKey, report)
	}
	return report
}
