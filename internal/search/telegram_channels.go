// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	_ "embed"
	"strings"
)

// PanSou issue #4, updated 2026-10-09, retrieved 2026-10-17:
// https://github.com/fish2018/pansou/issues/4
// Its heading says 110, but the CHANNELS value contains 111 unique usernames.
// This reviewed snapshot is bundled locally; startup never fetches the issue.
//
//go:embed telegram_channels.txt
var telegramChannelSnapshot string

func defaultTelegramChannels() []string {
	return strings.Fields(telegramChannelSnapshot)
}
