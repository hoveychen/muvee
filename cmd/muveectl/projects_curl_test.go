package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSendProxyRequest_DoesNotFollowRedirects locks in that `projects curl`
// surfaces the container's 3xx verbatim instead of following it. A root-relative
// Location (e.g. /login) would otherwise be resolved by the HTTP client against
// the muvee API host (…/api/projects/<id>/proxy/…) and silently hit the muvee
// server itself rather than the container — the "proxy behaves abnormally" bug.
func TestSendProxyRequest_DoesNotFollowRedirects(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/api/projects/ID/proxy/dashboard" {
			w.Header().Set("Location", "/login")
			w.WriteHeader(http.StatusFound) // 302 with a root-relative Location
			return
		}
		// Reached only if the client followed the redirect (/login resolves
		// against this test server's host).
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	resp, err := c.sendProxyRequest("GET", srv.URL+"/api/projects/ID/proxy/dashboard", nil, nil)
	if err != nil {
		t.Fatalf("sendProxyRequest error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 — redirect must be surfaced, not followed", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Errorf("Location = %q, want /login", loc)
	}
	if len(hits) != 1 {
		t.Errorf("server hit %d times %v, want 1 — client must not follow the redirect", len(hits), hits)
	}
}
