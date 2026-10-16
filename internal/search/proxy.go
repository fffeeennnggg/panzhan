// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func proxyURLFromServer(server, requestScheme string) (*url.URL, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, nil
	}
	selected := ""
	configured := false
	for _, rawEntry := range strings.Split(server, ";") {
		entry := strings.TrimSpace(rawEntry)
		key, value, hasKey := strings.Cut(entry, "=")
		if !hasKey {
			if !configured {
				selected = entry
			}
			continue
		}
		configured = true
		if strings.EqualFold(strings.TrimSpace(key), requestScheme) {
			selected = strings.TrimSpace(value)
		}
	}
	if selected == "" {
		return nil, nil
	}
	if !strings.Contains(selected, "://") {
		selected = "http://" + selected
	}
	proxy, err := url.Parse(selected)
	if err != nil || proxy.Host == "" {
		return nil, fmt.Errorf("系统代理地址无效")
	}
	return proxy, nil
}

func searchProxy(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}

func proxyBypassed(req *http.Request, list string) bool {
	host := strings.TrimSuffix(strings.ToLower(req.URL.Hostname()), ".")
	port := req.URL.Port()
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, rawEntry := range strings.Split(list, ",") {
		entry := strings.TrimSpace(strings.ToLower(rawEntry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "/") {
			if ip := net.ParseIP(host); ip != nil {
				if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
					return true
				}
			}
			continue
		}
		if proxyHost, proxyPort, err := net.SplitHostPort(entry); err == nil {
			if strings.Trim(proxyHost, "[]") == host && proxyPort == port {
				return true
			}
			continue
		}
		entry = strings.TrimPrefix(strings.TrimPrefix(entry, "*."), ".")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}
