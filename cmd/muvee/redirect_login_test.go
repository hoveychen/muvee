package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRedirectToLogin_PreservesProjectSubdomain verifies that the
// forward-auth login redirect lands on the original project subdomain
// rather than the apex domain. Previously redirectToLogin hard-coded
// forwardAuthBase ("https://example.com"), which made handleLoginPage
// look up the project by apex host, fail, and fall back to the full
// provider set — silently bypassing each project's enabled_providers
// whitelist.
func TestRedirectToLogin_PreservesProjectSubdomain(t *testing.T) {
	prevBase := forwardAuthBase
	prevCookieDomain := cookieDomain
	forwardAuthBase = "https://example.com"
	cookieDomain = "example.com"
	t.Cleanup(func() {
		forwardAuthBase = prevBase
		cookieDomain = prevCookieDomain
	})

	r := httptest.NewRequest(http.MethodGet, "/verify", nil)
	r.Header.Set("X-Forwarded-Host", "myproj.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Uri", "/dashboard")
	w := httptest.NewRecorder()

	redirectToLogin(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	got := w.Header().Get("Location")
	want := "https://myproj.example.com/_oauth/login"
	if got != want {
		t.Fatalf("login redirect lost project subdomain:\n  got:  %s\n  want: %s", got, want)
	}
}

// redirectCookieValue returns the value of the fwd_oauth_redirect cookie set
// on the response, or "" if the cookie was not set (or was cleared).
func redirectCookieValue(w *httptest.ResponseRecorder) (string, bool) {
	for _, c := range w.Result().Cookies() {
		if c.Name == "fwd_oauth_redirect" {
			if c.MaxAge < 0 {
				return "", false
			}
			return c.Value, true
		}
	}
	return "", false
}

// TestRedirectToLogin_SkipsRedirectCookieForSubresource reproduces the
// "sometimes lands on /favicon.ico after login" bug. Browsers auto-request
// /favicon.ico for every navigated page; that request is also unauthenticated
// and also hits forward-auth, so redirectToLogin would clobber the
// fwd_oauth_redirect cookie previously set for the real page with
// "/favicon.ico". After OAuth completes the callback reads the cookie and
// bounces the user to /favicon.ico. A sub-resource request (Sec-Fetch-Dest
// != "document") must NOT write the redirect cookie.
func TestRedirectToLogin_SkipsRedirectCookieForSubresource(t *testing.T) {
	prevBase := forwardAuthBase
	prevCookieDomain := cookieDomain
	forwardAuthBase = "https://example.com"
	cookieDomain = "example.com"
	t.Cleanup(func() {
		forwardAuthBase = prevBase
		cookieDomain = prevCookieDomain
	})

	r := httptest.NewRequest(http.MethodGet, "/verify", nil)
	r.Header.Set("X-Forwarded-Host", "myproj.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Uri", "/favicon.ico")
	r.Header.Set("Sec-Fetch-Dest", "image")
	w := httptest.NewRecorder()

	redirectToLogin(w, r)

	if val, ok := redirectCookieValue(w); ok {
		t.Fatalf("favicon sub-resource request must not set fwd_oauth_redirect, but got %q", val)
	}
}

// TestRedirectToLogin_SetsRedirectCookieForDocumentNav confirms the happy path
// still works: a top-level document navigation records the full original URL so
// the post-OAuth callback returns the user to the page they wanted.
func TestRedirectToLogin_SetsRedirectCookieForDocumentNav(t *testing.T) {
	prevBase := forwardAuthBase
	prevCookieDomain := cookieDomain
	forwardAuthBase = "https://example.com"
	cookieDomain = "example.com"
	t.Cleanup(func() {
		forwardAuthBase = prevBase
		cookieDomain = prevCookieDomain
	})

	r := httptest.NewRequest(http.MethodGet, "/verify", nil)
	r.Header.Set("X-Forwarded-Host", "myproj.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Uri", "/dashboard")
	r.Header.Set("Sec-Fetch-Dest", "document")
	w := httptest.NewRecorder()

	redirectToLogin(w, r)

	val, ok := redirectCookieValue(w)
	if !ok {
		t.Fatalf("document navigation must set fwd_oauth_redirect cookie")
	}
	if want := "https://myproj.example.com/dashboard"; val != want {
		t.Fatalf("redirect cookie:\n  got:  %s\n  want: %s", val, want)
	}
}

// TestRedirectToLogin_NoForwardedHostFallsBackToBase keeps the existing
// behaviour for the edge case where Traefik did not supply
// X-Forwarded-Host (e.g. direct hits during local dev). In that case we
// have nothing better than forwardAuthBase to redirect to.
func TestRedirectToLogin_NoForwardedHostFallsBackToBase(t *testing.T) {
	prevBase := forwardAuthBase
	prevCookieDomain := cookieDomain
	forwardAuthBase = "https://example.com"
	cookieDomain = "example.com"
	t.Cleanup(func() {
		forwardAuthBase = prevBase
		cookieDomain = prevCookieDomain
	})

	r := httptest.NewRequest(http.MethodGet, "/verify", nil)
	w := httptest.NewRecorder()

	redirectToLogin(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", w.Code)
	}
	got := w.Header().Get("Location")
	want := "https://example.com/_oauth/login"
	if got != want {
		t.Fatalf("fallback redirect:\n  got:  %s\n  want: %s", got, want)
	}
}
