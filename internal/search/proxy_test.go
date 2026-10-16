// SPDX-License-Identifier: AGPL-3.0-or-later
package search

import (
	"net/http"
	"net/url"
	"testing"
)

func TestWindowsProxyForRequestUsesSchemeSpecificProxy(t *testing.T) {
	target, err := url.Parse("https://search.example/path")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := proxyURLFromServer("http=proxy.local:8080;https=secure-proxy.local:8443", target.Scheme)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := proxy.String(), "http://secure-proxy.local:8443"; got != want {
		t.Fatalf("proxy = %q, want %q", got, want)
	}
}

func TestWindowsProxyForRequestUsesDefaultProxy(t *testing.T) {
	target, err := url.Parse("https://search.example/path")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := proxyURLFromServer("127.0.0.1:10808", target.Scheme)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := proxy.String(), "http://127.0.0.1:10808"; got != want {
		t.Fatalf("proxy = %q, want %q", got, want)
	}
}

func TestProxyBypassedMatchesHostSuffixAndCIDR(t *testing.T) {
	target, err := url.Parse("https://api.internal.example/path")
	if err != nil {
		t.Fatal(err)
	}
	if !proxyBypassed(&http.Request{URL: target}, ".internal.example,10.0.0.0/8") {
		t.Fatal("expected internal hostname to bypass proxy")
	}
	target, err = url.Parse("https://10.12.3.4/path")
	if err != nil {
		t.Fatal(err)
	}
	if !proxyBypassed(&http.Request{URL: target}, ".internal.example,10.0.0.0/8") {
		t.Fatal("expected private network address to bypass proxy")
	}
}

func TestProxyBypassedAlwaysIncludesLoopback(t *testing.T) {
	target, err := url.Parse("http://127.0.0.1:12345/search")
	if err != nil {
		t.Fatal(err)
	}
	if !proxyBypassed(&http.Request{URL: target}, "") {
		t.Fatal("expected local loopback to bypass system proxy")
	}
}
