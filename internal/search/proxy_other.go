// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build !windows

package search

import (
	"net/http"
	"net/url"
)

func platformSearchProxy(req *http.Request) (*url.URL, error) {
	return searchProxy(req)
}
