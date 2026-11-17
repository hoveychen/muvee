package main

import (
	"net/http/httptest"
	"testing"
)

// TestUserInfoCORSSameOriginOnly locks in BACKLOG-20261117-007: /_oauth/userinfo
// (and the SDK endpoints routed through applyUserInfoCORS) echo Origin only for
// the subsite's own origin. Another subsite of the same base domain is
// same-site, so its credentialed fetch would carry this subsite's session;
// it must not be allowed to read the reply.
func TestUserInfoCORSSameOriginOnly(t *testing.T) {
	prevDomain, prevBases, prevBase := cookieDomain, cookieBaseDomains, forwardAuthBase
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	forwardAuthBase = "https://muveeai.com"
	t.Cleanup(func() { cookieDomain, cookieBaseDomains, forwardAuthBase = prevDomain, prevBases, prevBase })

	tests := []struct {
		origin   string
		wantEcho bool
	}{
		{"https://foo.muvee.ai", true},       // own origin
		{"https://FOO.muvee.ai", true},       // host case-insensitive
		{"https://bar.muvee.ai", false},      // sibling subsite, same site
		{"https://muvee.ai", false},          // base apex
		{"https://foo.muveeai.com", false},   // same label, other base
		{"http://foo.muvee.ai", false},       // wrong scheme
		{"https://foo.muvee.ai:8443", false}, // port
		{"https://evil.example.org", false},
		{"null", false},
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
		if w.Header().Get("Vary") != "Origin" {
			t.Errorf("origin %q: Vary = %q, want Origin", tt.origin, w.Header().Get("Vary"))
		}
	}
}
