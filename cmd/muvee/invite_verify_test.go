package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// namedCookie returns the Set-Cookie the response emitted for cookieName, and
// whether it was present at all. A cookie with MaxAge < 0 is a *clear* (the
// browser deletes it) — callers distinguish set-vs-clear via the returned
// *http.Cookie's MaxAge.
func namedCookie(w *httptest.ResponseRecorder, cookieName string) (*http.Cookie, bool) {
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			return c, true
		}
	}
	return nil, false
}

// TestHandleVerify_UnauthedInviteNav_CapturesInviteToken pins the happy path of
// the invitation link: an unauthenticated top-level navigation carrying
// ?invite_token=... must (1) stash the token in the muvee_invite_token cookie
// so handleOAuthCallback can consume it after the OAuth round-trip, and (2)
// preserve the full original URL (invite_token included) in fwd_oauth_redirect
// so the user lands back on the invited page — NOT on /favicon.ico. This is the
// end-to-end contract the favicon-redirect fix was supposed to keep intact.
func TestHandleVerify_UnauthedInviteNav_CapturesInviteToken(t *testing.T) {
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
	r.Header.Set("X-Forwarded-Uri", "/dashboard?invite_token=TOK123")
	r.Header.Set("Sec-Fetch-Dest", "document")
	w := httptest.NewRecorder()

	handleVerify(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("unauthenticated invite navigation should redirect to login, got %d", w.Code)
	}

	inv, ok := namedCookie(w, inviteTokenCookieName)
	if !ok {
		t.Fatalf("invite navigation must set %s cookie so the callback can consume the link", inviteTokenCookieName)
	}
	if inv.Value != "TOK123" {
		t.Fatalf("%s cookie value:\n  got:  %q\n  want: %q", inviteTokenCookieName, inv.Value, "TOK123")
	}
	if inv.MaxAge <= 0 {
		t.Fatalf("%s cookie must be a live set (MaxAge>0), got MaxAge=%d", inviteTokenCookieName, inv.MaxAge)
	}

	// The return URL must retain invite_token so the post-OAuth landing is the
	// invited page, not /favicon.ico.
	red, ok := namedCookie(w, "fwd_oauth_redirect")
	if !ok {
		t.Fatalf("document navigation must record fwd_oauth_redirect return URL")
	}
	if want := "https://myproj.example.com/dashboard?invite_token=TOK123"; red.Value != want {
		t.Fatalf("fwd_oauth_redirect return URL:\n  got:  %s\n  want: %s", red.Value, want)
	}
}

// TestHandleVerify_FaviconSubresource_LeavesInviteCookieUntouched is the
// regression guard for the invitation-link-vs-favicon interaction.
//
// When a browser opens an invite link, it navigates to the page (which sets
// muvee_invite_token, see the test above) AND auto-fetches /favicon.ico. That
// favicon request is equally unauthenticated and equally hits forward-auth. It
// carries no invite_token. handleVerify must therefore emit NO Set-Cookie for
// muvee_invite_token on the favicon hop — neither a fresh set nor a clear —
// otherwise it would wipe out the token captured by the navigation and the
// OAuth callback would have nothing to consume, silently breaking the invite.
//
// (The sibling favicon fix already proves the favicon hop won't clobber
// fwd_oauth_redirect; this test proves the same hop won't clobber the
// *separate* invite-token cookie that the invitation flow actually relies on.)
func TestHandleVerify_FaviconSubresource_LeavesInviteCookieUntouched(t *testing.T) {
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

	handleVerify(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("unauthenticated favicon fetch should still redirect to login, got %d", w.Code)
	}
	if c, ok := namedCookie(w, inviteTokenCookieName); ok {
		t.Fatalf("favicon sub-resource must not touch %s (would wipe the invited token), "+
			"but response set it to %q with MaxAge=%d", inviteTokenCookieName, c.Value, c.MaxAge)
	}
}
