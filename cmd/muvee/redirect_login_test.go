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
