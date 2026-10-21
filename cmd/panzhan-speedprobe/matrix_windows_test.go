// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Account API calls initialize the shared transport's HTTP/2 TLS settings.
// Forcing HTTP/1 on a clone must also reset the already-populated ALPN list.
func TestMatrixHTTP1AfterHTTP2Initialization(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	server.EnableHTTP2 = true
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	template := http.DefaultTransport.(*http.Transport).Clone()
	template.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	template.ForceAttemptHTTP2 = true
	defer template.CloseIdleConnections()
	res, err := (&http.Client{Transport: template}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.ProtoMajor != 2 {
		t.Fatalf("warmup used %s, wanted HTTP/2", res.Proto)
	}
	for _, protocol := range []string{"h1", "h2", "auto"} {
		t.Run(protocol, func(t *testing.T) {
			transport := matrixHTTPTransport(template, protocol)
			defer transport.CloseIdleConnections()
			res, err := (&http.Client{Transport: transport}).Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			want := 2
			if protocol == "h1" {
				want = 1
			}
			if res.ProtoMajor != want {
				t.Fatalf("got %s for %s", res.Proto, protocol)
			}
		})
	}
}
