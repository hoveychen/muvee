package main

import (
	"net/http/httptest"
	"testing"
)

// TestAuthCookieDomainFollowsRequestHost locks in the multi-domain contract:
// an auth cookie must be scoped to whichever configured base domain the
// request arrived on, so a login on muvee.ai yields a `.muvee.ai` cookie
// rather than the canonical `.muveeai.com`. Uses handleFwdLogout because it
// sets the forward-auth session cookie with no OAuth/provider dependencies.
func TestAuthCookieDomainFollowsRequestHost(t *testing.T) {
	prevDomain, prevBases := cookieDomain, cookieBaseDomains
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	t.Cleanup(func() { cookieDomain, cookieBaseDomains = prevDomain, prevBases })

	tests := []struct {
		host string
		want string
	}{
		{"app.muvee.ai", "muvee.ai"},      // second base domain
		{"foo.muveeai.com", "muveeai.com"}, // canonical base domain
		{"1.2.3.4", "muveeai.com"},         // unmatched host -> canonical fallback
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", "https://"+tt.host+"/_oauth/logout", nil)
		req.Host = tt.host
		w := httptest.NewRecorder()

		handleFwdLogout(w, req)

		var got string
		for _, c := range w.Result().Cookies() {
			if c.Name == "muvee_fwd_session" {
				got = c.Domain
			}
		}
		if got != tt.want {
			t.Errorf("host %q: cookie Domain = %q, want %q", tt.host, got, tt.want)
		}
	}
}
