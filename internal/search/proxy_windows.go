// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build windows

package search

import (
	"net/http"
	"net/url"
	"os"

	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func platformSearchProxy(req *http.Request) (*url.URL, error) {
	if proxyBypassed(req, os.Getenv("NO_PROXY")) || proxyBypassed(req, os.Getenv("no_proxy")) {
		return nil, nil
	}
	proxy, err := searchProxy(req)
	if err != nil || proxy != nil || hasEnvironmentProxy(req.URL.Scheme) {
		return proxy, err
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return nil, nil
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return nil, nil
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		return nil, nil
	}
	return proxyURLFromServer(server, req.URL.Scheme)
}

func hasEnvironmentProxy(scheme string) bool {
	if _, ok := os.LookupEnv("ALL_PROXY"); ok {
		return true
	}
	if scheme == "https" {
		_, upper := os.LookupEnv("HTTPS_PROXY")
		_, lower := os.LookupEnv("https_proxy")
		return upper || lower
	}
	_, upper := os.LookupEnv("HTTP_PROXY")
	_, lower := os.LookupEnv("http_proxy")
	return upper || lower
}
