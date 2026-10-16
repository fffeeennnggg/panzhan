package search

import (
	"context"
	"testing"
	"time"
)

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
