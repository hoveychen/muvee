package main

import (
	"net/http/httptest"
	"testing"
)

// TestUserInfoCORSMultiDomain locks in the P5 contract for authservice:
// /_oauth/userinfo (and the other SDK endpoints that route through
// applyUserInfoCORS) must echo the Origin for a subdomain of ANY configured
// base domain, so a project SPA on foo.muvee.ai can fetch it cross-origin with
// credentials — not just subdomains of the canonical cookieDomain.
func TestUserInfoCORSMultiDomain(t *testing.T) {
	prevDomain, prevBases := cookieDomain, cookieBaseDomains
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	t.Cleanup(func() { cookieDomain, cookieBaseDomains = prevDomain, prevBases })

	tests := []struct {
		origin    string
		wantEcho  bool
	}{
		{"https://foo.muveeai.com", true}, // canonical base subdomain
		{"https://foo.muvee.ai", true},    // second base-domain subdomain
		{"https://muvee.ai", true},        // second base apex
		{"https://evil.example.org", false},
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", "https://foo.muvee.ai/_oauth/userinfo", nil)
		req.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()

		applyUserInfoCORS(w, req)

		got := w.Header().Get("Access-Control-Allow-Origin")
		if tt.wantEcho && got != tt.origin {
			t.Errorf("origin %q: Access-Control-Allow-Origin = %q, want it echoed", tt.origin, got)
		}
		if !tt.wantEcho && got != "" {
			t.Errorf("origin %q: expected no CORS echo, got %q", tt.origin, got)
		}
	}
}
