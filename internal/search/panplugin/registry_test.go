// SPDX-License-Identifier: AGPL-3.0-or-later
package panplugin

import (
	"slices"
	"testing"

	"panzhan/internal/search/panplugin/plugin"
)

func TestIssue4RegistryMatchesManifest(t *testing.T) {
	want := append([]string(nil), Issue4PluginNames...)
	slices.Sort(want)
	got := make([]string, 0, len(plugin.GetRegisteredPlugins()))
	for _, source := range plugin.GetRegisteredPlugins() {
		got = append(got, source.Name())
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("registered plugins differ from issue #4 manifest:\n got %v\nwant %v", got, want)
	}
}
