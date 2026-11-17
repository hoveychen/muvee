package main

import (
	"net/http/httptest"
	"testing"
)

// -007: 会话 cookie muvee_fwd_session 的 Domain 跟随请求 host 所在基域（`.muvee.ai` vs `.muveeai.com`）→
// 会话 cookie 是 host-only 的 __Host-muvee_fwd（任何情况下都不带 Domain，不跨子站共享）；
// 多基域契约只剩「清除旧基域 cookie 时 Domain 跟随请求 host 的基域」，由 legacy muvee_fwd_session 的删除 cookie 体现。
// 用 handleFwdLogout 因为它不依赖 OAuth/provider 即可同时写出这两类 cookie。
func TestAuthCookieDomainFollowsRequestHost(t *testing.T) {
	prevDomain, prevBases := cookieDomain, cookieBaseDomains
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	t.Cleanup(func() { cookieDomain, cookieBaseDomains = prevDomain, prevBases })

	tests := []struct {
		host string
		want string // Domain of the legacy-cookie deletion
	}{
		{"app.muvee.ai", "muvee.ai"},       // second base domain
		{"foo.muveeai.com", "muveeai.com"}, // canonical base domain
		{"1.2.3.4", "muveeai.com"},         // unmatched host -> canonical fallback
	}
	for _, tt := range tests {
		req := httptest.NewRequest("GET", "https://"+tt.host+"/_oauth/logout", nil)
		req.Host = tt.host
		w := httptest.NewRecorder()

		handleFwdLogout(w, req)

		cookies := w.Result().Cookies()
		// New session cookie: cleared, host-only, never tied to a base domain.
		sess := findCookie(cookies, fwdCookieName(fwdSessionCookieBase))
		if sess == nil || sess.MaxAge >= 0 || sess.Domain != "" {
			t.Errorf("host %q: __Host-muvee_fwd clear = %+v, want a deletion with no Domain", tt.host, sess)
		}
		// Legacy base-domain session cookie: deleted on the request's base domain.
		old := findCookie(cookies, "muvee_fwd_session")
		if old == nil || old.MaxAge >= 0 || old.Domain != tt.want {
			t.Errorf("host %q: legacy muvee_fwd_session clear = %+v, want deletion with Domain=%q", tt.host, old, tt.want)
		}
		// No cookie carrying a live value may be scoped to a base domain.
		for _, c := range cookies {
			if c.Value != "" && c.MaxAge >= 0 && c.Domain != "" {
				t.Errorf("host %q: live cookie %q written with Domain=%q", tt.host, c.Name, c.Domain)
			}
		}
	}
}
