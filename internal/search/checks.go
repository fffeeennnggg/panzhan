// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"panzhan/internal/quark"
	"time"
)

func (e *Engine) RememberChecks(checks []quark.ShareCheck) error {
	if e.catalog == nil {
		return nil
	}
	c := e.catalog
	c.mu.Lock()
	defer c.mu.Unlock()
	next := make(map[string]Result, len(c.items))
	for k, v := range c.items {
		next[k] = v
	}
	for _, check := range checks {
		if check.CheckedAt == "" {
			continue
		}
		r, ok := normalizeResult(Result{URL: check.URL}, Source{}, c.now())
		if !ok {
			continue
		}
		old, ok := next[r.ID]
		if !ok || old.CheckedAt > check.CheckedAt {
			continue
		}
		currentShare, _ := quark.ParseShare(old.URL)
		checkedShare, _ := quark.ParseShare(check.URL)
		if currentShare.Code != checkedShare.Code && check.Availability != "invalid" {
			continue
		}
		old.CheckedAt, old.Availability = check.CheckedAt, check.Availability
		old.CheckMessage, old.FileCount = check.Message, check.FileCount
		old.Content = check.Content
		next[r.ID] = old
	}
	if err := c.save(next); err != nil {
		return err
	}
	c.items = next
	return nil
}

// ApplyChecks also overlays source-response caches so a rediscovered link cannot
// resurrect a known invalid result or erase a more recent verification.
func (e *Engine) ApplyChecks(report Report) Report {
	if e.catalog == nil {
		return report
	}
	c := e.catalog
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Result, 0, len(report.Results))
	for _, r := range report.Results {
		if saved, ok := c.items[r.ID]; ok && saved.CheckedAt >= r.CheckedAt {
			r.CheckedAt, r.Availability = saved.CheckedAt, saved.Availability
			r.CheckMessage, r.FileCount = saved.CheckMessage, saved.FileCount
			r.Content = saved.Content
		}
		checked, _ := time.Parse(time.RFC3339Nano, r.CheckedAt)
		if r.Availability == "invalid" && c.now().Sub(checked) < 10*time.Minute {
			continue
		}
		out = append(out, r)
	}
	report.Results = out
	return report
}
