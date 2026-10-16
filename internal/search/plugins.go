// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"panzhan/internal/quark"
	"panzhan/internal/search/panplugin"
	"panzhan/internal/search/panplugin/plugin"
)

// SourcePlugin describes a single independently scheduled search source.
type SourcePlugin interface {
	ID() string
	Name() string
	Priority() int
	NeedsConfig() bool
	Search(context.Context, string) ([]Result, error)
}

type PanSouResult struct {
	Title    string
	Datetime time.Time
	Links    []PanSouLink
}

type PanSouLink struct {
	Type      string
	URL       string
	Password  string
	WorkTitle string
	Datetime  time.Time
}

type panSouAdapter struct {
	source Source
	plugin plugin.AsyncSearchPlugin
}

func (p panSouAdapter) ID() string        { return p.source.ID }
func (p panSouAdapter) Name() string      { return p.source.Name }
func (p panSouAdapter) Priority() int     { return p.source.Priority }
func (p panSouAdapter) NeedsConfig() bool { return p.source.NeedsConfig }

func registeredPanSouPlugins() []struct {
	Source Source
	plugin SourcePlugin
} {
	registered := plugin.GetRegisteredPlugins()
	byName := make(map[string]plugin.AsyncSearchPlugin, len(registered))
	for _, item := range registered {
		byName[item.Name()] = item
	}
	entries := make([]struct {
		Source Source
		plugin SourcePlugin
	}, 0, len(panplugin.Issue4PluginNames))
	for _, name := range panplugin.Issue4PluginNames {
		item := byName[name]
		if item == nil {
			panic(fmt.Sprintf("PanSou issue #4 plugin %q was not registered", name))
		}
		displayName := name
		if named, ok := item.(interface{ DisplayName() string }); ok && strings.TrimSpace(named.DisplayName()) != "" {
			displayName = named.DisplayName()
		}
		source := Source{ID: "panplugin:" + name, Name: displayName, Priority: item.Priority(), NeedsConfig: panSouNeedsConfig(name)}
		entries = append(entries, struct {
			Source Source
			plugin SourcePlugin
		}{
			Source: source,
			plugin: panSouAdapter{source: source, plugin: item},
		})
	}
	return entries
}

func panSouNeedsConfig(name string) bool {
	switch name {
	case "gying", "panlian", "qqpd", "weibo", "woniu":
		return true
	default:
		return false
	}
}

func (p panSouAdapter) Search(ctx context.Context, query string) ([]Result, error) {
	const contextKey = "_ctx"
	items, err := p.plugin.Search(query, map[string]interface{}{contextKey: ctx})
	if err != nil {
		return nil, err
	}
	results := make([]PanSouResult, 0, len(items))
	for _, item := range items {
		converted := PanSouResult{Title: item.Title, Datetime: item.Datetime}
		for _, link := range item.Links {
			converted.Links = append(converted.Links, PanSouLink{Type: link.Type, URL: link.URL, Password: link.Password, WorkTitle: link.WorkTitle, Datetime: link.Datetime})
		}
		results = append(results, converted)
	}
	return ConvertPanSouResults(p.source.ID, p.source.Name, results), nil
}

// ConvertPanSouResults keeps only valid HTTPS Quark shares and maps PanSou's
// structured metadata into Panzhan's existing resource model.
func ConvertPanSouResults(sourceID, sourceName string, results []PanSouResult) []Result {
	now := time.Now()
	out := make([]Result, 0)
	positions := make(map[string]int)
	hasWorkTitle := make(map[string]bool)
	for _, result := range results {
		for _, link := range result.Links {
			if !strings.EqualFold(strings.TrimSpace(link.Type), "quark") {
				continue
			}
			u, err := url.Parse(strings.TrimSpace(link.URL))
			if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "pan.quark.cn") || u.User != nil {
				continue
			}
			share, err := quark.ParseShare(u.String())
			if err != nil {
				continue
			}
			if share.Code == "" && strings.TrimSpace(link.Password) != "" {
				query := u.Query()
				query.Set("pwd", strings.TrimSpace(link.Password))
				u.RawQuery = query.Encode()
			}
			title := strings.TrimSpace(link.WorkTitle)
			if title == "" {
				title = strings.TrimSpace(result.Title)
			}
			published := link.Datetime
			if published.IsZero() {
				published = result.Datetime
			}
			r := Result{Title: title, URL: u.String(), Source: sourceName}
			if !published.IsZero() {
				r.PublishedAt = published.UTC().Format(time.RFC3339)
			}
			if normalized, ok := normalizeResult(r, Source{ID: sourceID, Name: sourceName}, now); ok {
				if index, exists := positions[normalized.ID]; exists {
					merged := mergeResult(out[index], normalized)
					if hasWorkTitle[normalized.ID] {
						merged.Title = out[index].Title
					}
					out[index] = merged
				} else {
					positions[normalized.ID] = len(out)
					hasWorkTitle[normalized.ID] = strings.TrimSpace(link.WorkTitle) != ""
					out = append(out, normalized)
				}
			}
		}
	}
	return out
}
