package search

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Opt-in read-only availability probe. A successful test execution does not imply sources returned results.
func TestLiveSources(t *testing.T) {
	if os.Getenv("PANZHAN_LIVE_SEARCH") != "1" {
		t.Skip("live source probe is opt-in")
	}
	query := os.Getenv("PANZHAN_LIVE_QUERY")
	if query == "" {
		query = "Blender"
	}
	sources := strings.Split(os.Getenv("PANZHAN_LIVE_SOURCES"), ",")
	if len(sources) == 1 && sources[0] == "" {
		sources = []string{"pansearch"}
	}
	r := New().Search(context.Background(), query, sources)
	t.Logf("sources=%+v, results=%d", r.Sources, len(r.Results))
	if len(r.Results) > 0 {
		t.Logf("first title: %s", r.Results[0].Title)
	}
}
