package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The post-login return URL (host-only __Host-fwd_oauth_redirect) is single-use
// and lives 10 minutes. A user who reaches a project's /_oauth/login without a
// fresh one (back button after a completed login, a bookmarked login page, an
// app that links straight to /_oauth/login) used to be bounced to "/" on the
// OAuth callback host — the platform apex, i.e. the console — after signing in.
//
// -007: seedLoginReturnURL 在登录页回填「项目根」绝对地址（基域 Domain cookie，因为回调落在 apex）→
// 登录完成点已经是子站自己的 /_oauth/session，回跳 "/" 就是子站根，不再需要回填：
// 登录页不写回跳 cookie，没有 cookie 时 takePostLoginRedirect 返回相对 "/"（留在子站，永远到不了控制台）。
func TestTakePostLoginRedirect_NoCookieStaysOnSubsite(t *testing.T) {
	prevBase, prevDomain, prevBases := forwardAuthBase, cookieDomain, cookieBaseDomains
	forwardAuthBase = "https://app.muveeai.com"
	cookieDomain = "muveeai.com"
	cookieBaseDomains = []string{"muveeai.com", "muvee.ai"}
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, cookieBaseDomains = prevBase, prevDomain, prevBases
	})

	tests := []struct {
		name         string
		host         string
		cookie       string // value of __Host-fwd_oauth_redirect; "" = none
		wantLocation string
		wantCleared  bool
	}{
		{"project subdomain, no cookie", "proj.muvee.ai", "", "/", false},
		{"project subdomain canonical, no cookie", "proj.muveeai.com", "", "/", false},
		{"existing same-host return URL kept", "proj.muvee.ai", "https://proj.muvee.ai/deep/link", "https://proj.muvee.ai/deep/link", true},
		{"existing relative return URL kept", "proj.muvee.ai", "/deep/link?x=1", "/deep/link?x=1", true},
		{"return URL of another host on the same base refused", "proj.muvee.ai", "https://other.muvee.ai/deep", "/", true},
		{"return URL of the other base domain refused", "proj.muvee.ai", "https://proj.muveeai.com/deep", "/", true},
		{"return URL pointing at the auth host (console) refused", "proj.muvee.ai", "https://app.muvee.ai/", "/", true},
		{"unknown base, no cookie", "proj.other.com", "", "/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/_oauth/session", nil)
			r.Host = tt.host
			r.Header.Set("X-Forwarded-Host", tt.host)
			r.Header.Set("X-Forwarded-Proto", "https")
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdRedirectCookieBase), Value: tt.cookie})
			}
			w := httptest.NewRecorder()

			got := takePostLoginRedirect(w, r)

			if got != tt.wantLocation {
				t.Fatalf("takePostLoginRedirect = %q, want %q", got, tt.wantLocation)
			}
			c := findCookie(w.Result().Cookies(), fwdCookieName(fwdRedirectCookieBase))
			if tt.wantCleared != (c != nil) {
				t.Fatalf("cookie written = %+v, want cleared=%v (single use)", c, tt.wantCleared)
			}
			if c != nil && (c.MaxAge >= 0 || c.Domain != "" || c.Path != "/") {
				t.Fatalf("return cookie not deleted host-only: %+v", c)
			}
		})
	}
}

// -007: seedLoginReturnURL 的「有 cookie 就不覆盖 / 无 cookie 写项目根」→ 登录页自己从不写回跳 cookie
// （写回跳 cookie 的只有 handleVerify→redirectToLogin 的文档导航，见 invite_verify_test.go），
// 所以走完整登录页的一跳（provider 起点）既不创建、也不改写、也不清除已有回跳 cookie。
func TestLoginPage_LeavesReturnCookieAlone(t *testing.T) {
	withSingleGoogleProject(t)
	withStateSecret(t)
	for _, have := range []bool{false, true} {
		r := subsiteReq("datamap.sigma-fi.com")
		r.URL = &url.URL{Path: "/_oauth/login", RawQuery: "provider=google"}
		if have {
			r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdRedirectCookieBase), Value: "https://datamap.sigma-fi.com/deep/link"})
		}
		w := httptest.NewRecorder()
		handleLoginPage(w, r)
		if w.Code != http.StatusFound {
			t.Fatalf("have=%v: status=%d", have, w.Code)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == fwdCookieName(fwdRedirectCookieBase) || c.Name == "fwd_oauth_redirect" {
				t.Fatalf("have=%v: login page touched the return cookie: %+v", have, c)
			}
		}
	}
}
