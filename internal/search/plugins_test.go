package search

import (
	"context"
	"errors"
	"net/http"
	"panzhan/internal/search/panplugin/model"
	"panzhan/internal/search/panplugin/plugin"
	"testing"
	"time"
)

// Exercise both upstream APIs: some plugins discard IsFinal in Search, while
// others use AsyncSearch directly. Neither may return a successful empty reply
// at PanSou's 4s background-response window in Panzhan.
func TestPanSouAdapterWaitsForLateResults(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "plain"
		if structured {
			name = "structured"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := &fixturePanSouPlugin{
				BaseAsyncPlugin: plugin.NewBaseAsyncPlugin(t.Name(), 1), structured: structured,
				search: func(_ *http.Client, _ string, ext map[string]interface{}) ([]model.SearchResult, error) {
					timer := time.NewTimer(4100 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
						return panSouFixtureResults(), nil
					case <-plugin.ContextFromExt(ext).Done():
						return nil, plugin.ContextFromExt(ext).Err()
					}
				},
			}
			s := Source{ID: "panplugin:fixture", Name: "Fixture"}
			e := NewWithSources([]Source{s})
			e.panSouPlugins = map[string]SourcePlugin{s.ID: panSouAdapter{source: s, plugin: p}}
			got := e.SearchSource(context.Background(), "late", s.ID, "")
			if len(got.Results) != 1 || got.Sources[0].Error != "" {
				t.Fatalf("late result was lost: %+v", got)
			}
		})
	}
}

func TestPanSouAdapterDeadlineIsNotCachedAsEmptySuccess(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "plain"
		if structured {
			name = "structured"
		}
		t.Run(name, func(t *testing.T) {
			p := &fixturePanSouPlugin{
				BaseAsyncPlugin: plugin.NewBaseAsyncPlugin(t.Name(), 1), structured: structured,
				search: func(_ *http.Client, _ string, ext map[string]interface{}) ([]model.SearchResult, error) {
					<-plugin.ContextFromExt(ext).Done()
					return nil, plugin.ContextFromExt(ext).Err()
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			s := Source{ID: "panplugin:fixture", Name: "Fixture"}
			adapter := panSouAdapter{source: s, plugin: p}
			if _, err := adapter.Search(ctx, "retry"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline must be reported as failure, got %v", err)
			}
			p.search = func(*http.Client, string, map[string]interface{}) ([]model.SearchResult, error) {
				return panSouFixtureResults(), nil
			}
			got, err := adapter.Search(context.Background(), "retry")
			if err != nil || len(got) != 1 {
				t.Fatalf("retry was poisoned by an empty response: %+v, %v", got, err)
			}
		})
	}
}

type fixturePanSouPlugin struct {
	*plugin.BaseAsyncPlugin
	structured bool
	search     func(*http.Client, string, map[string]interface{}) ([]model.SearchResult, error)
}

func (p *fixturePanSouPlugin) Search(query string, ext map[string]interface{}) ([]model.SearchResult, error) {
	if p.structured {
		result, err := p.AsyncSearchWithResult(query, p.search, "", ext)
		return result.Results, err
	}
	return p.AsyncSearch(query, p.search, "", ext)
}

func panSouFixtureResults() []model.SearchResult {
	return []model.SearchResult{{UniqueID: "late", Title: "Late result", Links: []model.Link{{Type: "quark", URL: "https://pan.quark.cn/s/late123"}}}}
}

func TestPanSouResultConversion(t *testing.T) {
	got := ConvertPanSouResults("panplugin:demo", "Demo", []PanSouResult{{
		Title: "结果标题",
		Links: []PanSouLink{
			{Type: "quark", URL: "https://pan.quark.cn/s/share01", Password: "1234", WorkTitle: "作品标题"},
			{Type: "baidu", URL: "https://pan.baidu.com/s/ignored", Password: "abcd"},
			{Type: "quark", URL: "http://pan.quark.cn/s/insecure"},
			{Type: "quark", URL: "https://evil.example/s/unsafe"},
			{Type: "quark", URL: "https://pan.quark.cn/s/share01?pwd=1234", WorkTitle: "重复分享"},
		},
	}})
	if len(got) != 1 {
		t.Fatalf("want one safe Quark result, got %+v", got)
	}
	if got[0].Title != "作品标题" || got[0].URL != "https://pan.quark.cn/s/share01?pwd=1234" {
		t.Fatalf("unexpected converted result: %+v", got[0])
	}
	if got[0].Source != "Demo" || len(got[0].Origins) != 1 || got[0].Origins[0].ID != "panplugin:demo" {
		t.Fatalf("source metadata was not preserved: %+v", got[0])
	}
}

func TestSourcePluginContractCarriesPriorityAndSearch(t *testing.T) {
	var plugin SourcePlugin = testSourcePlugin{}
	if plugin.ID() != "test" || plugin.Name() != "Test" || plugin.Priority() != 5 {
		t.Fatalf("unexpected source metadata: %q %q %d", plugin.ID(), plugin.Name(), plugin.Priority())
	}
	got, err := plugin.Search(context.Background(), "query")
	if err != nil || len(got) != 1 || got[0].Title != "query" {
		t.Fatalf("unexpected source result: %+v, %v", got, err)
	}
}

func TestSourcePrioritySortIsStable(t *testing.T) {
	results := []Result{
		{Title: "ordinary-1", Origins: []Origin{{ID: "ordinary"}}},
		{Title: "priority", Origins: []Origin{{ID: "priority"}}},
		{Title: "ordinary-2", Origins: []Origin{{ID: "ordinary"}}},
	}
	sortResultsByPriority(results, []Source{{ID: "ordinary", Priority: 1}, {ID: "priority", Priority: 5}})
	if results[0].Title != "priority" || results[1].Title != "ordinary-1" || results[2].Title != "ordinary-2" {
		t.Fatalf("unexpected stable priority order: %+v", results)
	}
}

type testSourcePlugin struct{}

func (testSourcePlugin) ID() string        { return "test" }
func (testSourcePlugin) Name() string      { return "Test" }
func (testSourcePlugin) Priority() int     { return 5 }
func (testSourcePlugin) NeedsConfig() bool { return false }
func (testSourcePlugin) Search(_ context.Context, query string) ([]Result, error) {
	return []Result{{Title: query, DiscoveredAt: time.Now().UTC().Format(time.RFC3339)}}, nil
}
