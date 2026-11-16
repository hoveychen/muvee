package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The post-login return URL (fwd_oauth_redirect) is single-use and only lives
// 5 minutes. A user who reaches a project's /_oauth/login without a fresh one
// (back button after a completed login, a bookmarked login page, an app that
// links straight to /_oauth/login) used to be bounced to "/" on the OAuth
// callback host — the platform apex, i.e. the console — after signing in.
// seedLoginReturnURL backfills the project subdomain's root instead.
func TestSeedLoginReturnURL(t *testing.T) {
	prevBase, prevDomain, prevBases := forwardAuthBase, cookieDomain, cookieBaseDomains
	forwardAuthBase = "https://app.muveeai.com"
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, cookieBaseDomains = prevBase, prevDomain, prevBases
	})

	tests := []struct {
		name       string
		host       string
		haveCookie bool
		wantValue  string // "" = no cookie written
		wantDomain string
	}{
		{"project subdomain, no cookie", "proj.muvee.ai", false, "https://proj.muvee.ai/", "muvee.ai"},
		{"project subdomain canonical", "proj.muveeai.com", false, "https://proj.muveeai.com/", "muveeai.com"},
		{"existing return URL kept", "proj.muvee.ai", true, "", ""},
		{"apex is the console", "muvee.ai", false, "", ""},
		{"canonical auth host", "app.muveeai.com", false, "", ""},
		{"rebased auth host", "app.muvee.ai", false, "", ""},
		{"unknown base", "proj.other.com", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/_oauth/login?provider=google", nil)
			r.Host = tt.host
			r.Header.Set("X-Forwarded-Host", tt.host)
			r.Header.Set("X-Forwarded-Proto", "https")
			if tt.haveCookie {
				r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://proj.muvee.ai/deep/link"})
			}
			w := httptest.NewRecorder()

			seedLoginReturnURL(w, r)

			var got *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == "fwd_oauth_redirect" {
					got = c
				}
			}
			if tt.wantValue == "" {
				if got != nil {
					t.Fatalf("unexpected fwd_oauth_redirect=%q", got.Value)
				}
				return
			}
			if got == nil {
				t.Fatalf("no fwd_oauth_redirect cookie written")
			}
			if got.Value != tt.wantValue || got.Domain != tt.wantDomain {
				t.Fatalf("cookie = %q (Domain=%q), want %q (Domain=%q)", got.Value, got.Domain, tt.wantValue, tt.wantDomain)
			}
		})
	}
}
