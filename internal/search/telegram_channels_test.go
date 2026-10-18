package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestTelegramIssue4Snapshot(t *testing.T) {
	channels := defaultTelegramChannels()
	if len(channels) != 111 {
		t.Fatalf("issue #4 CHANNELS snapshot: got %d channels, want 111", len(channels))
	}
	valid := regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)
	seen := map[string]bool{}
	for _, channel := range channels {
		key := strings.ToLower(channel)
		if !valid.MatchString(channel) || seen[key] {
			t.Fatalf("invalid or duplicate Telegram username: %q", channel)
		}
		seen[key] = true
	}
	for _, required := range []string{"tgsearchers7", "MCPH02", "MCPH03", "BooksRealm", "Panzi88com"} {
		if !seen[strings.ToLower(required)] {
			t.Errorf("missing upstream channel %s", required)
		}
	}
	if seen["tgsearchers3"] {
		t.Error("superseded legacy list was appended instead of replaced")
	}
	for _, source := range New().Sources() {
		if source.ID != "telegram" {
			continue
		}
		if len(source.Channels) != len(channels) || len(source.PriorityChannels) != 5 {
			t.Fatalf("source does not expose current channel snapshot: %+v", source)
		}
		priority := map[string]bool{}
		for _, channel := range source.PriorityChannels {
			key := strings.ToLower(channel)
			if !seen[key] || priority[key] {
				t.Errorf("priority channel missing from snapshot or duplicated: %s", channel)
			}
			priority[key] = true
		}
		return
	}
	t.Fatal("Telegram source missing")
}

func TestNewIssue4ChannelCanBeScheduled(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/s/Panzi88com" {
			t.Errorf("unexpected channel request %s", r.URL.Path)
		}
		w.Write([]byte(`<div class="tgme_widget_message_wrap"><div class="tgme_widget_message" data-post="Panzi88com/1"><div class="tgme_widget_message_text">Blender https://pan.quark.cn/s/issue4fixture</div></div></div>`))
	}))
	defer upstream.Close()
	for _, source := range New().Sources() {
		if source.ID != "telegram" {
			continue
		}
		source.Base = upstream.URL + "/s"
		engine := NewWithSources([]Source{source})
		report := engine.SearchSource(context.Background(), "Blender", "telegram", "Panzi88com")
		if requests != 1 || len(report.Results) != 1 || len(report.Sources) != 1 || report.Sources[0].Error != "" {
			t.Fatalf("newly synced channel cannot run: requests=%d report=%+v", requests, report)
		}
		return
	}
	t.Fatal("Telegram source missing")
}
