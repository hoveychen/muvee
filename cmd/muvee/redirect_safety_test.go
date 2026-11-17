package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// withRedirectDomains pins the platform base domains for a test: two aliases,
// mirroring a BASE_DOMAINS=opencws.top,sigma-fi.com deployment.
func withRedirectDomains(t *testing.T) {
	t.Helper()
	prevBase, prevDomain, prevBases := forwardAuthBase, cookieDomain, cookieBaseDomains
	forwardAuthBase = "https://opencws.top"
	cookieDomain = "opencws.top"
	cookieBaseDomains = []string{"opencws.top", "sigma-fi.com"}
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, cookieBaseDomains = prevBase, prevDomain, prevBases
	})
}

// safeReturnURL is the single gate every user-influenced redirect target goes
// through (logout ?redirect= and the fwd_oauth_redirect cookie consumed after
// sign-in). It must keep the user on this platform.
func TestSafeReturnURL(t *testing.T) {
	withRedirectDomains(t)
	tests := []struct {
		raw  string
		host string // inbound host
		ok   bool
	}{
		{"/", "datamap.sigma-fi.com", true},
		{"/deep/link?x=1#f", "datamap.sigma-fi.com", true},
		{"https://datamap.sigma-fi.com/after", "datamap.sigma-fi.com", true},
		{"https://other.opencws.top/", "datamap.sigma-fi.com", true},
		{"https://sigma-fi.com/", "datamap.sigma-fi.com", true},
		{"http://proj.opencws.top/", "proj.opencws.top", false},    // platform runs on https
		{"https://tunnel.cfdemo.com/x", "tunnel.cfdemo.com", true}, // same host, outside bases
		{"", "proj.opencws.top", false},
		{"//evil.com", "proj.opencws.top", false},
		{"//evil.com/path", "proj.opencws.top", false},
		{"/\\evil.com", "proj.opencws.top", false},
		{"\\\\evil.com", "proj.opencws.top", false},
		{"/\t/evil.com", "proj.opencws.top", false},
		{"/\n/evil.com", "proj.opencws.top", false},
		{"https://evil.com/", "proj.opencws.top", false},
		{"https://evilopencws.top/", "proj.opencws.top", false},
		{"https://opencws.top.evil.com/", "proj.opencws.top", false},
		{"https://user@evil.com/", "proj.opencws.top", false},
		{"https://opencws.top@evil.com/", "proj.opencws.top", false},
		{"https://evil.com\\@proj.opencws.top/", "proj.opencws.top", false},
		{"javascript:alert(1)", "proj.opencws.top", false},
		{"data:text/html,x", "proj.opencws.top", false},
		{"evil.com", "proj.opencws.top", false},
		{"ftp://proj.opencws.top/", "proj.opencws.top", false},
		{"https:evil.com", "proj.opencws.top", false},
		{"https:/evil.com", "proj.opencws.top", false},
	}
	for _, tt := range tests {
		got, ok := safeReturnURL(tt.raw, tt.host)
		if ok != tt.ok {
			t.Errorf("safeReturnURL(%q, %q) ok=%v (got %q), want ok=%v", tt.raw, tt.host, ok, got, tt.ok)
		}
		if ok && got != tt.raw {
			t.Errorf("safeReturnURL(%q) rewrote to %q", tt.raw, got)
		}
	}
}

func doLogout(t *testing.T, host, query string) *http.Response {
	t.Helper()
	r := httptest.NewRequest("GET", "/_oauth/logout"+query, nil)
	r.Host = host
	r.Header.Set("X-Forwarded-Host", host)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handleFwdLogout(w, r)
	return w.Result()
}

// Plain-http return URLs are only accepted when the platform itself is served
// over http (local dev); in production they would hand the fresh session to
// port 80 in clear text.
func TestSafeReturnURL_HTTPOnlyOnHTTPPlatform(t *testing.T) {
	withRedirectDomains(t)
	forwardAuthBase = "http://opencws.top"
	if _, ok := safeReturnURL("http://proj.opencws.top/", "proj.opencws.top"); !ok {
		t.Fatal("http return URL refused on an http platform")
	}
}

func TestFwdLogout_RedirectTargets(t *testing.T) {
	withRedirectDomains(t)
	tests := []struct {
		name, host, query, want string
	}{
		{"default lands on logged-out page", "datamap.sigma-fi.com", "", "/_oauth/logged-out"},
		{"relative path kept", "datamap.sigma-fi.com", "?redirect=/bye", "/bye"},
		{"same-platform absolute kept", "datamap.sigma-fi.com", "?redirect=https://www.opencws.top/", "https://www.opencws.top/"},
		{"external host refused", "datamap.sigma-fi.com", "?redirect=https://evil.com/", "/_oauth/logged-out"},
		{"protocol-relative refused", "datamap.sigma-fi.com", "?redirect=//evil.com", "/_oauth/logged-out"},
		{"backslash trick refused", "datamap.sigma-fi.com", "?redirect=/%5Cevil.com", "/_oauth/logged-out"},
		{"tab trick refused", "datamap.sigma-fi.com", "?redirect=/%09/evil.com", "/_oauth/logged-out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := doLogout(t, tt.host, tt.query)
			if res.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", res.StatusCode)
			}
			if got := res.Header.Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

// -007: 登出清除 muvee_fwd_session，Domain=请求所在基域 → 登出清除的是 host-only 的 __Host-muvee_fwd
// （Max-Age<0、无 Domain、Path=/、HttpOnly、Secure）；旧的基域 muvee_fwd_session 仍会按请求所在基域被删除，
// 让升级前遗留的 cookie 不再随请求带给租户。
func TestFwdLogout_ClearsSessionHostOnlyAndLegacyOnRequestBaseDomain(t *testing.T) {
	withRedirectDomains(t)
	for host, wantLegacyDomain := range map[string]string{
		"datamap.sigma-fi.com": "sigma-fi.com",
		"proj.opencws.top":     "opencws.top",
	} {
		res := doLogout(t, host, "?redirect=https://evil.com/")
		got := findCookie(res.Cookies(), "__Host-muvee_fwd")
		if got == nil || got.MaxAge >= 0 || got.Value != "" || got.Domain != "" || got.Path != "/" || !got.HttpOnly || !got.Secure {
			t.Fatalf("%s: session cookie not cleared host-only: %+v", host, got)
		}
		old := findCookie(res.Cookies(), "muvee_fwd_session")
		if old == nil || old.MaxAge >= 0 || old.Value != "" || old.Domain != wantLegacyDomain || old.Path != "/" {
			t.Fatalf("%s: legacy cookie not deleted on %s: %+v", host, wantLegacyDomain, old)
		}
	}
}

// The post-login return URL is read from the subsite's own host-only cookie
// (__Host-fwd_oauth_redirect) by takePostLoginRedirect, which only follows
// same-host targets and deletes the cookie.
//
// -007: postLoginRedirect(r) 在回调主机（基域）读基域共享 cookie，允许任意平台域名（含别名基域的其他项目）→
// takePostLoginRedirect(w, r) 在子站读 host-only cookie，只允许本站（同 host）路径/绝对地址，其它平台域名一律退回 "/"；
// 旧名 fwd_oauth_redirect 的 cookie 完全不读。
func TestPostLoginRedirect(t *testing.T) {
	withRedirectDomains(t)
	tests := []struct {
		name, cookie, want string
	}{
		{"no cookie", "", "/"},
		{"same-host page kept", "https://datamap.sigma-fi.com/deep?x=1", "https://datamap.sigma-fi.com/deep?x=1"},
		{"relative kept", "/deep?x=1", "/deep?x=1"},
		{"other platform host refused (was: alias base kept)", "https://proj.opencws.top/", "/"},
		{"sibling refused", "https://evil.sigma-fi.com/phish", "/"},
		{"tossed external refused", "https://evil.com/phish", "/"},
		{"tossed protocol-relative refused", "//evil.com", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := subsiteReq("datamap.sigma-fi.com")
			r.URL = &url.URL{Path: "/_oauth/session"}
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdRedirectCookieBase), Value: tt.cookie})
			}
			w := httptest.NewRecorder()
			if got := takePostLoginRedirect(w, r); got != tt.want {
				t.Fatalf("takePostLoginRedirect = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("legacy shared cookie name ignored", func(t *testing.T) {
		r := subsiteReq("datamap.sigma-fi.com")
		r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://datamap.sigma-fi.com/deep"})
		if got := takePostLoginRedirect(httptest.NewRecorder(), r); got != "/" {
			t.Fatalf("legacy cookie followed: %q", got)
		}
	})
	t.Run("single use", func(t *testing.T) {
		r := subsiteReq("datamap.sigma-fi.com")
		r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdRedirectCookieBase), Value: "/deep"})
		w := httptest.NewRecorder()
		takePostLoginRedirect(w, r)
		if c := findCookie(w.Result().Cookies(), fwdCookieName(fwdRedirectCookieBase)); c == nil || c.MaxAge >= 0 {
			t.Fatalf("return cookie not deleted after use: %+v", c)
		}
	})
}

// The password / SMS handlers must route their final redirect through
// takePostLoginRedirect. Drive the real SMS-less path: password login success.
//
// -007: 被甩入的回跳 cookie 名 fwd_oauth_redirect（基域）→ __Host-fwd_oauth_redirect（子站 host-only），
// 断言不变（不跟随 evil.com），并补充落点就是本站 "/"。
func TestPasswordLogin_TossedReturnCookieRefused(t *testing.T) {
	withRedirectDomains(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/internal/projects/by-host"):
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "p1", "password_login": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "email": "u@example.com", "name": "u", "user_id": "u1"})
		}
	}))
	defer upstream.Close()
	prevServer, prevSecret := muveeServerURL, jwtSecret
	muveeServerURL = upstream.URL
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() { muveeServerURL, jwtSecret = prevServer, prevSecret })

	r := httptest.NewRequest("POST", "/_oauth/password", strings.NewReader("username=u&password=p"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Host = "datamap.sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
	r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdRedirectCookieBase), Value: "https://evil.com/phish"})
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://evil.com/phish"})
	w := httptest.NewRecorder()
	handlePasswordLogin(w, r)
	res := w.Result()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || strings.Contains(loc, "evil.com") || loc != "/" {
		t.Fatalf("status=%d Location=%q; tossed return URL must not be followed", res.StatusCode, loc)
	}
}

// The logged-out page is served on the project's own host (Traefik routes
// /_oauth/* there straight to authservice, bypassing ForwardAuth). It must
// render — never redirect — so a single-provider project does not silently
// sign the user straight back in through the provider's still-live SSO.
func TestLoggedOutPage(t *testing.T) {
	withRedirectDomains(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id": "p1", "project_name": "datamap",
			"enabled_providers": "google",
			"branding":          map[string]any{"site_name": "Sigma DataMap", "primary_color": "#123456"},
		})
	}))
	defer upstream.Close()
	prevServer := muveeServerURL
	muveeServerURL = upstream.URL
	t.Cleanup(func() { muveeServerURL = prevServer })

	for _, lang := range []string{"zh-CN", "en-US"} {
		r := httptest.NewRequest("GET", "/_oauth/logged-out", nil)
		r.Host = "datamap.sigma-fi.com"
		r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
		r.Header.Set("Accept-Language", lang)
		w := httptest.NewRecorder()
		handleLoggedOutPage(w, r)
		res := w.Result()
		if res.StatusCode != http.StatusOK || res.Header.Get("Location") != "" {
			t.Fatalf("%s: status=%d Location=%q, want a rendered 200", lang, res.StatusCode, res.Header.Get("Location"))
		}
		if len(res.Cookies()) != 0 {
			t.Fatalf("%s: logged-out page must not set cookies, got %v", lang, res.Cookies())
		}
		body := w.Body.String()
		// -007: 「重新登录」链接 /_oauth/login → /_oauth/login?sso=none（点击后跳过静默中心登录，
		// 否则中心 SSO 仍有效时会立刻被静默登回）。
		for _, want := range []string{"Sigma DataMap", "#123456", `href="/_oauth/login?sso=none"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s: body missing %q", lang, want)
			}
		}
		wantCopy := map[string]string{"zh-CN": "重新登录", "en-US": "Sign in again"}[lang]
		if !strings.Contains(body, wantCopy) {
			t.Fatalf("%s: body missing %q", lang, wantCopy)
		}
		// No provider buttons / forms / auto-submit on this page.
		for _, bad := range []string{"provider=google", "<form", "http-equiv=\"refresh\""} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s: logged-out page must not contain %q", lang, bad)
			}
		}
	}
}

// The request-access page's sign-out link must land on the logged-out page,
// not "/" (which would auto-redirect a single-provider project back into SSO).
func TestRequestAccessSignOutLink(t *testing.T) {
	if strings.Contains(requestAccessPageTmpl, "/_oauth/logout?redirect=/") {
		t.Fatal("request-access sign-out link still redirects to / (auto re-login)")
	}
	if !strings.Contains(requestAccessPageTmpl, `href="/_oauth/logout"`) {
		t.Fatal("request-access sign-out link missing")
	}
}

// Drift guard: the return-URL cookie decides where a fresh session lands, so
// it is only read inside takePostLoginRedirect (which vets it with
// sameHostReturnURL and deletes it). A new handler that reads it directly must
// go through takePostLoginRedirect instead — update this guard only alongside
// such a review.
//
// -007: 旧守卫扫描 authservice.go 中 `.Cookie("fwd_oauth_redirect")` 的读取点（postLoginRedirect +
// seedLoginReturnURL 共 2 处）→ 现在只允许 takePostLoginRedirect 通过 hostCookie(r, fwdRedirectCookieBase)
// 读取（恰好 1 处，扫描 authservice.go / fwd_sso.go / login_token.go），并且旧名 fwd_oauth_redirect 的
// 任何读取（r.Cookie("fwd_oauth_redirect")）都不得存在。
func TestFwdOAuthRedirectCookieReadOnlyViaVettedHelpers(t *testing.T) {
	var readers []string
	for _, file := range []string{"authservice.go", "fwd_sso.go", "login_token.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		fn := ""
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(line, "func ") {
				fn = line
			}
			if strings.Contains(line, `.Cookie("fwd_oauth_redirect")`) {
				t.Errorf("%s: legacy fwd_oauth_redirect cookie is read in %q", file, fn)
			}
			if strings.Contains(line, "hostCookie(") && strings.Contains(line, "fwdRedirectCookieBase") {
				readers = append(readers, fn)
			}
		}
	}
	allowed := "func takePostLoginRedirect(w http.ResponseWriter, r *http.Request) string {"
	for _, f := range readers {
		if f != allowed {
			t.Errorf("return-URL cookie read outside the vetted helper in %q; use takePostLoginRedirect", f)
		}
	}
	if len(readers) != 1 {
		t.Errorf("expected exactly 1 vetted read, found %d: %v", len(readers), readers)
	}
}
