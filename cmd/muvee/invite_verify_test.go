package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestHandleVerify_UnauthedInviteNav_KeepsInviteTokenInReturnURL pins the happy
// path of the invitation link: an unauthenticated top-level navigation carrying
// ?invite_token=... must preserve the full original URL (invite_token included)
// in the host-only return-URL cookie so the user lands back on the invited page
// — NOT on /favicon.ico — and the first /verify with the new session consumes
// the token in place.
//
// -007: 邀请 token 写入基域 cookie muvee_invite_token，由 OAuth 回调消费 →
// 不再有邀请 cookie：token 只随回跳 URL（__Host-fwd_oauth_redirect，query 保留 invite_token）回来，
// 登录后在 /verify 就地消费（handleVerify 的 inviteToken 分支）。回跳 URL 的断言保持原语义，仅 cookie 名/属性变化。
func TestHandleVerify_UnauthedInviteNav_KeepsInviteTokenInReturnURL(t *testing.T) {
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

	// The invite token no longer rides in a cookie of its own (neither the old
	// base-domain name nor a host-only one): it would be one more attacker
	// writable carrier. It travels in the return URL only.
	for _, c := range w.Result().Cookies() {
		if c.Name == "muvee_invite_token" || c.Name == "__Host-muvee_invite_token" || strings.Contains(c.Name, "invite") {
			t.Fatalf("invite navigation must not set an invite cookie, got %+v", c)
		}
	}

	// The return URL must retain invite_token so the post-OAuth landing is the
	// invited page, not /favicon.ico — in the subsite's own host-only cookie.
	red, ok := namedCookie(w, fwdCookieName(fwdRedirectCookieBase))
	if !ok {
		t.Fatalf("document navigation must record the %s return URL", fwdCookieName(fwdRedirectCookieBase))
	}
	if want := "https://myproj.example.com/dashboard?invite_token=TOK123"; red.Value != want {
		t.Fatalf("return URL:\n  got:  %s\n  want: %s", red.Value, want)
	}
	if red.Name != "__Host-fwd_oauth_redirect" || red.Domain != "" || red.Path != "/" || !red.Secure || !red.HttpOnly || red.MaxAge <= 0 {
		t.Fatalf("return cookie is not a live host-only __Host- cookie: %+v", red)
	}
	if _, ok := namedCookie(w, "fwd_oauth_redirect"); ok {
		t.Fatal("legacy base-domain fwd_oauth_redirect was written")
	}
	// The login URL stays on the project host (so the project's provider
	// whitelist applies) and does not carry the token itself.
	if loc := w.Header().Get("Location"); loc != "https://myproj.example.com/_oauth/login" {
		t.Fatalf("Location = %q, want the project's own login page", loc)
	}
	// ...and the sanitised return URL still yields the invited page on the same host.
	if got := sameHostReturnURL(red.Value, "myproj.example.com"); got != red.Value {
		t.Fatalf("return URL does not survive the same-host check: %q", got)
	}
}

// TestHandleVerify_FaviconSubresource_LeavesReturnCookieUntouched is the
// regression guard for the invitation-link-vs-favicon interaction.
//
// When a browser opens an invite link, it navigates to the page (which records
// the return URL carrying invite_token, see the test above) AND auto-fetches
// /favicon.ico. That favicon request is equally unauthenticated and equally
// hits forward-auth. It carries no invite_token. handleVerify must therefore
// emit NO Set-Cookie for the return-URL cookie on the favicon hop — neither a
// fresh set nor a clear — otherwise it would wipe out (or replace with
// /favicon.ico) the URL captured by the navigation and the invite token riding
// in it would never come back to /verify, silently breaking the invite.
//
// -007: 保护对象由「独立的 muvee_invite_token cookie」变为「携带 invite_token 的回跳 cookie
// __Host-fwd_oauth_redirect」（邀请 cookie 已不存在，仍断言任何 invite cookie 都不会出现）。
func TestHandleVerify_FaviconSubresource_LeavesReturnCookieUntouched(t *testing.T) {
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
	for _, name := range []string{fwdCookieName(fwdRedirectCookieBase), "fwd_oauth_redirect", "muvee_invite_token", "__Host-muvee_invite_token"} {
		if c, ok := namedCookie(w, name); ok {
			t.Fatalf("favicon sub-resource must not touch %s (would wipe the invited token's return URL), "+
				"but response set it to %q with MaxAge=%d", name, c.Value, c.MaxAge)
		}
	}
}
