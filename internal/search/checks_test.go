package search

import (
	"panzhan/internal/quark"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckPersistenceFiltersInvalidWithoutResurrectingSourceCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.json")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	e := NewWithSources(nil)
	e.catalog = c
	rows, err := c.Remember([]Result{{Title: "资源 good", URL: "https://pan.quark.cn/s/good"}, {Title: "资源 bad", URL: "https://pan.quark.cn/s/bad"}, {Title: "资源 uncertain", URL: "https://pan.quark.cn/s/uncertain"}}, Source{ID: "fixture", Name: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	checks := []quark.ShareCheck{}
	for i, availability := range []string{"accessible", "invalid", "unknown"} {
		checks = append(checks, quark.ShareCheck{URL: rows[i].URL, Availability: availability, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Message: availability})
	}
	checks[0].Content = &quark.ContentSummary{FileCount: 2, TotalBytes: 2048, Types: map[string]int{"video": 2}, Complete: true}
	if err := e.RememberChecks(checks); err != nil {
		t.Fatal(err)
	}
	got := e.ApplyChecks(Report{Results: rows})
	if len(got.Results) != 2 || got.Results[0].Availability != "accessible" || got.Results[1].Availability != "unknown" {
		t.Fatal(got)
	}
	if got.Results[0].Content == nil || got.Results[0].Content.TotalBytes != 2048 {
		t.Fatal("content metadata was not applied")
	}
	// A later source observation with no check must preserve the invalid verdict.
	c.Remember(rows, Source{ID: "fixture", Name: "Fixture"})
	restored, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Search("资源", []string{"fixture"}); len(got) != 2 {
		t.Fatal(got)
	}
	for _, r := range restored.Search("资源", []string{"fixture"}) {
		if r.Availability == "accessible" && (r.Content == nil || r.Content.TotalBytes != 2048) {
			t.Fatal("content metadata was lost after restart")
		}
	}
	// Invalid verdicts expire: rediscovered shares can be checked again.
	c.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if got := e.ApplyChecks(Report{Results: rows}); len(got.Results) != 3 {
		t.Fatal(got)
	}
}
