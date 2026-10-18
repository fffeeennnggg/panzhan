// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"panzhan/internal/quark"
)

type Origin struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Catalog is bounded and separate from account/task state. All updates are
// serialized and committed through an atomic snapshot; failed writes roll back.
type Catalog struct {
	mu    sync.Mutex
	path  string
	items map[string]Result
	limit int
	now   func() time.Time
}
type catalogSnapshot struct {
	Version int      `json:"version"`
	Items   []Result `json:"items"`
}

func OpenCatalog(path string) (*Catalog, error) {
	c := &Catalog{path: path, items: map[string]Result{}, limit: 10000, now: time.Now}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<20 {
		return nil, fmt.Errorf("资源库超出 64 MiB，请保留文件并检查")
	}
	var snapshot catalogSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Version != 1 || len(snapshot.Items) > c.limit {
		return nil, fmt.Errorf("资源库损坏或版本不支持，请保留 resources.json 并恢复备份")
	}
	for _, r := range snapshot.Items {
		normalized, ok := normalizeResult(r, Source{}, c.now())
		if !ok {
			return nil, fmt.Errorf("资源库包含无效链接，请保留 resources.json 并检查")
		}
		c.items[normalized.ID] = normalized
	}
	return c, nil
}

func normalizeResult(r Result, source Source, now time.Time) (Result, bool) {
	share, err := quark.ParseShare(r.URL)
	if err != nil {
		return Result{}, false
	}
	r.ID = "quark:" + share.ID + ":" + share.Parent
	r.Provider = "quark"
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" {
		r.Title = "夸克分享"
	}
	if chars := []rune(r.Title); len(chars) > 500 {
		r.Title = string(chars[:500])
	}
	if r.Source == "" {
		r.Source = source.Name
	}
	if source.ID != "" {
		r.Origins = append(append([]Origin{}, r.Origins...), Origin{ID: source.ID, Name: source.Name})
	}
	r.Origins = mergeOrigins(nil, r.Origins)
	stamp := now.UTC().Format(time.RFC3339)
	if r.DiscoveredAt == "" {
		r.DiscoveredAt = stamp
	}
	if r.LastSeenAt == "" {
		r.LastSeenAt = stamp
	}
	if r.Availability == "" {
		r.Availability = "unknown"
	}
	r.Local = false
	r.Cached = false
	return r, true
}

func mergeOrigins(a, b []Origin) []Origin {
	merged := map[string]Origin{}
	for _, o := range append(append([]Origin{}, a...), b...) {
		if o.ID != "" {
			merged[o.ID] = o
		}
	}
	ids := make([]string, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Origin, 0, len(ids))
	for _, id := range ids {
		out = append(out, merged[id])
	}
	return out
}

func mergeResult(old, next Result) Result {
	if old.ID == "" {
		return next
	}
	next.Origins = mergeOrigins(old.Origins, next.Origins)
	// Prefer the newer observation, while keeping a known extraction code when
	// another source supplies the same share without its code.
	oldShare, _ := quark.ParseShare(old.URL)
	newShare, _ := quark.ParseShare(next.URL)
	if newShare.Code == "" && oldShare.Code != "" {
		next.URL = old.URL
	}
	if next.Title == "夸克分享" {
		next.Title = old.Title
	}
	if next.DiscoveredAt == "" || old.DiscoveredAt < next.DiscoveredAt {
		next.DiscoveredAt = old.DiscoveredAt
	}
	if old.LastSeenAt > next.LastSeenAt {
		next.LastSeenAt = old.LastSeenAt
	}
	if next.PublishedAt == "" {
		next.PublishedAt = old.PublishedAt
	}
	if old.CheckedAt > next.CheckedAt {
		next.CheckedAt = old.CheckedAt
		next.Availability = old.Availability
		next.CheckMessage = old.CheckMessage
		next.FileCount = old.FileCount
		next.Content = old.Content
	}
	// A corrected extraction code needs a new check; a previous password failure
	// must not poison this candidate's verification cache.
	if newShare.Code != "" && newShare.Code != oldShare.Code && next.Availability != "invalid" {
		next.CheckedAt, next.Availability, next.CheckMessage, next.FileCount = "", "unknown", "", 0
		next.Content = nil
	}
	return next
}

func mergeResults(results []Result) []Result {
	out := []Result{}
	indices := map[string]int{}
	for _, r := range results {
		if r.ID == "" {
			var ok bool
			r, ok = normalizeResult(r, Source{}, time.Now())
			if !ok {
				continue
			}
		}
		if i, ok := indices[r.ID]; ok {
			out[i] = mergeResult(out[i], r)
		} else {
			indices[r.ID] = len(out)
			out = append(out, r)
		}
	}
	return out
}

func (c *Catalog) Remember(results []Result, source Source) ([]Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	normalized := []Result{}
	for _, r := range results {
		if r, ok := normalizeResult(r, source, c.now()); ok {
			normalized = append(normalized, r)
		}
	}
	if len(normalized) == 0 {
		return normalized, nil
	}
	next := make(map[string]Result, len(c.items)+len(normalized))
	for k, v := range c.items {
		next[k] = v
	}
	out := []Result{}
	for _, r := range normalized {
		r = mergeResult(next[r.ID], r)
		next[r.ID] = r
		out = append(out, r)
	}
	if len(next) > c.limit {
		sorted := make([]Result, 0, len(next))
		for _, r := range next {
			sorted = append(sorted, r)
		}
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].LastSeenAt == sorted[j].LastSeenAt {
				return sorted[i].ID < sorted[j].ID
			}
			return sorted[i].LastSeenAt > sorted[j].LastSeenAt
		})
		for _, r := range sorted[c.limit:] {
			delete(next, r.ID)
		}
	}
	if err := c.save(next); err != nil {
		return mergeResults(out), err
	}
	c.items = next
	return mergeResults(out), nil
}

func (c *Catalog) save(items map[string]Result) error {
	snapshot := catalogSnapshot{Version: 1, Items: make([]Result, 0, len(items))}
	for _, r := range items {
		snapshot.Items = append(snapshot.Items, r)
	}
	sort.Slice(snapshot.Items, func(i, j int) bool { return snapshot.Items[i].ID < snapshot.Items[j].ID })
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".resources-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, c.path)
	}
	return err
}

func (c *Catalog) Search(query string, enabled []string) []Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []Result{}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return out
	}
	for _, r := range c.items {
		if r.Availability == "invalid" {
			checked, _ := time.Parse(time.RFC3339Nano, r.CheckedAt)
			if c.now().Sub(checked) < 10*time.Minute {
				continue
			}
		}
		origins := []Origin{}
		for _, o := range r.Origins {
			if slices.Contains(enabled, o.ID) {
				origins = append(origins, o)
			}
		}
		if len(origins) == 0 {
			continue
		}
		match := true
		for _, term := range terms {
			if !strings.Contains(strings.ToLower(r.Title), term) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		r.Origins = origins
		r.Source = origins[0].Name
		r.Local = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeenAt == out[j].LastSeenAt {
			return out[i].ID < out[j].ID
		}
		return out[i].LastSeenAt > out[j].LastSeenAt
	})
	if len(out) > 500 {
		out = out[:500]
	}
	return out
}

// MarkAccessible records a successful, authenticated read of this share. A
// timeout/credential error must never be interpreted as proof of expiration.
func (c *Catalog) MarkAccessible(raw string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := normalizeResult(Result{URL: raw}, Source{}, c.now())
	if !ok {
		return nil
	}
	old, ok := c.items[r.ID]
	if !ok {
		return nil
	}
	next := make(map[string]Result, len(c.items))
	for k, v := range c.items {
		next[k] = v
	}
	old.CheckedAt = c.now().UTC().Format(time.RFC3339)
	old.Availability = "accessible"
	next[r.ID] = old
	if err := c.save(next); err != nil {
		return err
	}
	c.items = next
	return nil
}

func NewPersistent(path string) (*Engine, error) {
	c, err := OpenCatalog(path)
	if err != nil {
		return nil, err
	}
	e := New()
	e.catalog = c
	return e, nil
}
func (e *Engine) Local(query string, enabled []string) Report {
	r := Report{Results: []Result{}, Sources: []Status{}}
	if e.catalog != nil {
		r.Results = e.catalog.Search(query, enabled)
	}
	r.Warning = e.catalogWarning
	return r
}
func (e *Engine) SetCatalogWarning(message string) { e.catalogWarning = message }
func (e *Engine) MarkAccessible(raw string) error {
	if e.catalog != nil {
		return e.catalog.MarkAccessible(raw)
	}
	return nil
}
