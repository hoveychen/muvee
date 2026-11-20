package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hoveychen/muvee/internal/auth"
)

func findCookie(cs []*http.Cookie, name string) *http.Cookie {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// hintCookieName is the wire name of the signed-out hint: the host-only
// __Host-muvee_fwd_signed_out. (signedOutHintCookie is the bare base name,
// which is also the name of the *legacy* base-domain cookie.)
func hintCookieName() string { return fwdCookieName(signedOutHintCookie) }

// Logout drops a "signed out" hint so that a stale tab whose auth-guard
// reloads (→ ForwardAuth → /_oauth/login) does not get bounced straight
// through a single provider's still-live SSO session.
//
// -007: 基域共享提示 cookie muvee_fwd_signed_out（Domain=基域）→ host-only __Host-muvee_fwd_signed_out
// （无 Domain，只对登出的那个子站生效）；同时按请求所在基域删除旧名 muvee_fwd_signed_out，
// 以免升级前遗留的共享提示继续影响其他子站。
func TestFwdLogout_SetsSignedOutHint(t *testing.T) {
	withRedirectDomains(t)
	for host, wantLegacyDomain := range map[string]string{
		"datamap.site-b.test": "site-b.test",
		"proj.site-a.test":    "site-a.test",
	} {
		for _, q := range []string{"", "?redirect=/bye", "?redirect=https://evil.com/"} {
			cs := doLogout(t, host, q).Cookies()
			c := findCookie(cs, hintCookieName())
			if c == nil {
				t.Fatalf("%s%s: no %s cookie", host, q, hintCookieName())
			}
			if c.Name != "__Host-muvee_fwd_signed_out" || c.Value == "" || c.MaxAge < 7*24*3600 || c.Domain != "" || c.Path != "/" ||
				!c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("%s%s: bad hint cookie %+v", host, q, c)
			}
			old := findCookie(cs, "muvee_fwd_signed_out")
			if old == nil || old.MaxAge >= 0 || old.Domain != wantLegacyDomain {
				t.Fatalf("%s%s: legacy shared hint not deleted on %s: %+v", host, q, wantLegacyDomain, old)
			}
		}
	}
}

func withSingleGoogleProject(t *testing.T) {
	t.Helper()
	withRedirectDomains(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id": "p1", "project_name": "datamap", "enabled_providers": "google",
		})
	}))
	t.Cleanup(upstream.Close)
	prevServer := muveeServerURL
	muveeServerURL = upstream.URL
	t.Cleanup(func() { muveeServerURL = prevServer })
	setProvidersForTest(t, "google")
}

func doLoginPage(t *testing.T, hint bool, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "/_oauth/login"+query, nil)
	r.Host = "datamap.site-b.test"
	r.Header.Set("X-Forwarded-Host", "datamap.site-b.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	if hint {
		r.AddCookie(&http.Cookie{Name: hintCookieName(), Value: "1"})
	}
	w := httptest.NewRecorder()
	handleLoginPage(w, r)
	return w
}

// Without the hint a bare /_oauth/login first tries the silent central sign-in;
// when that has nothing (sso=none) a single-provider project keeps
// auto-redirecting to the provider exactly as before.
//
// -007: 无提示时单 provider 项目访问 /_oauth/login 直接 302 到 provider（?provider=google）→
// 第一跳先静默去中心主机 /_oauth/authorize?prompt=none；中心无 SSO 回来后（sso=none）才自动 302 到
// 中心主机 /_oauth/start?provider=google（带 host 与子站 state），provider 自动发起的语义保持。
func TestLoginPage_SingleProviderAutoRedirectsWithoutHint(t *testing.T) {
	withSingleGoogleProject(t)
	withStateSecret(t)

	w := doLoginPage(t, false, "")
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Host != "site-b.test" || loc.Path != "/_oauth/authorize" ||
		loc.Query().Get("prompt") != "none" || loc.Query().Get("host") != "datamap.site-b.test" || loc.Query().Get("state") == "" {
		t.Fatalf("status=%d Location=%q, want 302 to the central silent authorize", w.Code, w.Header().Get("Location"))
	}

	w = doLoginPage(t, false, "?sso=none")
	loc, _ = url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Host != "site-b.test" || loc.Path != "/_oauth/start" ||
		loc.Query().Get("provider") != "google" || loc.Query().Get("host") != "datamap.site-b.test" || loc.Query().Get("state") == "" {
		t.Fatalf("sso=none: status=%d Location=%q, want 302 to central /_oauth/start?provider=google", w.Code, w.Header().Get("Location"))
	}
}

// After an explicit sign-out the login page must not ride the provider's
// still-live SSO back in: neither silently via the central host nor by
// auto-starting the single provider.
//
// -007: 有提示时渲染「选择登录方式」页（provider 按钮 href=/_oauth/login?provider=google）→
// 裸 /_oauth/login（本来会静默 authorize）渲染「已登出」页（200、不跳转、无 provider 按钮、不写子站 state）；
// 点「重新登录」进入 ?sso=none 后才渲染选择页（仍不自动发起 provider）。
func TestLoginPage_SignedOutHintShowsLoggedOutThenSelectionPage(t *testing.T) {
	withSingleGoogleProject(t)
	withStateSecret(t)

	w := doLoginPage(t, true, "")
	if w.Code != http.StatusOK || w.Header().Get("Location") != "" {
		t.Fatalf("status=%d Location=%q, want rendered 200", w.Code, w.Header().Get("Location"))
	}
	if strings.Contains(w.Body.String(), "provider=google") {
		t.Fatal("logged-out page must not carry a provider button")
	}
	// The link carries sso=none so clicking it cannot silently re-use the live
	// central SSO.
	if !strings.Contains(w.Body.String(), `href="/_oauth/login?sso=none"`) {
		t.Fatal("logged-out page missing the sign-in-again link")
	}
	if len(slotSetCookies(w, fwdStateCookieBase, false)) != 0 {
		t.Fatal("logged-out page started a sign-in (wrote a __Host-fwd_state_* slot)")
	}

	w = doLoginPage(t, true, "?sso=none")
	if w.Code != http.StatusOK || w.Header().Get("Location") != "" {
		t.Fatalf("sso=none: status=%d Location=%q, want rendered 200", w.Code, w.Header().Get("Location"))
	}
	if !strings.Contains(w.Body.String(), `href="/_oauth/login?provider=google"`) {
		t.Fatal("selection page missing the provider button")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", w.Header().Get("Cache-Control"))
	}
}

// A completed sign-in clears the hint so later session expiries go back to
// the seamless single-provider redirect.
func TestPasswordLogin_ClearsSignedOutHint(t *testing.T) {
	withRedirectDomains(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/internal/projects/by-host") {
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "p1", "password_login": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "email": "u@example.com", "name": "u", "user_id": "u1"})
	}))
	defer upstream.Close()
	prevServer, prevSecret := muveeServerURL, jwtSecret
	muveeServerURL = upstream.URL
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() { muveeServerURL, jwtSecret = prevServer, prevSecret })

	r := httptest.NewRequest("POST", "/_oauth/password", strings.NewReader("username=u&password=p"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Host = "datamap.site-b.test"
	r.Header.Set("X-Forwarded-Host", "datamap.site-b.test")
	r.AddCookie(&http.Cookie{Name: hintCookieName(), Value: "1"})
	w := httptest.NewRecorder()
	handlePasswordLogin(w, r)
	res := w.Result()
	if c := findCookie(res.Cookies(), hostCookieName(fwdSessionCookieBase)); c == nil || c.Value == "" {
		t.Fatalf("password login did not issue a session (status %d)", res.StatusCode)
	}
	c := findCookie(res.Cookies(), hintCookieName())
	if c == nil || c.MaxAge >= 0 || c.Domain != "" || c.Path != "/" {
		t.Fatalf("hint not cleared host-only: %+v", c)
	}
}

// The OAuth / SSO path completes on the subsite's /_oauth/session; that
// exchange also clears the hint, consumes the return URL and issues the
// host-only session.
func TestSSOSession_ClearsSignedOutHint(t *testing.T) {
	withRedirectDomains(t)
	withStateSecret(t)
	host := "datamap.site-b.test"
	w0 := httptest.NewRecorder()
	nonce := writeSubsiteState(w0, subsiteReq(host), 0, false)
	code := mintSSOCode(&ssoCode{
		Host: host, Aud: "project:p1", StateDigest: stateDigest(nonce), SSOSID: "sso-1", AuthTime: time.Now().Unix(),
		ID: fwdIdentity{UserID: "u1", Email: "u@example.com", Name: "u", Provider: "google"},
	})
	r := subsiteReq(host)
	r.URL = &url.URL{Path: "/_oauth/session", RawQuery: url.Values{"code": {code}, "state": {nonce}}.Encode()}
	r.Header.Set("X-Muvee-Aud", "project:p1")
	carryCookies(w0, r)
	r.AddCookie(&http.Cookie{Name: hintCookieName(), Value: "1"})
	r.AddCookie(&http.Cookie{Name: hostCookieName(fwdRedirectCookieBase), Value: "/deep?x=1"})
	w := httptest.NewRecorder()
	handleSSOSession(w, r)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/deep?x=1" {
		t.Fatalf("status=%d Location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	sess := findCookie(w.Result().Cookies(), hostCookieName(fwdSessionCookieBase))
	if sess == nil || sess.Value == "" || sess.Domain != "" {
		t.Fatalf("session cookie %+v", sess)
	}
	if c, err := parseFwdToken(sess.Value, "project:p1", fwdKindSession); err != nil || c.SSOSID != "sso-1" {
		t.Fatalf("session claims=%+v err=%v", c, err)
	}
	if c := findCookie(w.Result().Cookies(), hintCookieName()); c == nil || c.MaxAge >= 0 || c.Domain != "" {
		t.Fatalf("hint not cleared host-only: %+v", c)
	}
}

// Drift guard: every sign-in that issues a fresh subsite session cookie must go
// through issueFwdSession (which also clears the signed-out hint and the legacy
// cookies). Only the silent renewal of an already-valid session may write the
// cookie directly.
//
// -007: 扫描字面量 "muvee_fwd_session" 的 http.Cookie{Name:...} 写入 → 会话 cookie 现在经
// setHostCookie(w, fwdSessionCookieBase, <value>, ...) 写入；扫描 authservice.go / login_token.go /
// fwd_sso.go 中所有以 fwdSessionCookieBase 写入非空值的行，只允许 issueFwdSession 与 renewSessionCookie；
// 另外禁止任何地方再按旧名 "muvee_fwd_session" 构造会话 cookie（旧名只允许出现在 legacyFwdCookies 的清除名单里）。
func TestFwdSessionIssuedOnlyViaIssueFwdSession(t *testing.T) {
	for _, file := range []string{"authservice.go", "login_token.go", "fwd_sso.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		fn := ""
		for _, line := range lines {
			if strings.HasPrefix(line, "func ") {
				fn = line
			}
			if strings.Contains(line, "Name:") && strings.Contains(line, `"muvee_fwd_session"`) {
				t.Errorf("%s: legacy session cookie name constructed in %q", file, fn)
			}
			if !strings.Contains(line, "setHostCookie(") || !strings.Contains(line, "fwdSessionCookieBase") {
				continue
			}
			if strings.Contains(line, `, "", -1)`) {
				continue // clearing, not issuing
			}
			switch {
			case strings.HasPrefix(fn, "func issueFwdSession("),
				strings.HasPrefix(fn, "func renewSessionCookie("):
			default:
				t.Errorf("%s: subsite session cookie issued directly in %q; use issueFwdSession", file, fn)
			}
		}
	}
}

// setProvidersForTest installs a real Google provider (dummy credentials) as
// the global forward-auth provider set for the duration of the test.
func setProvidersForTest(t *testing.T, _ string) {
	t.Helper()
	t.Setenv("GOOGLE_CLIENT_ID", "cid_test")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret_test")
	provs, err := auth.NewForwardAuthProviders(forwardAuthBase)
	if err != nil {
		t.Fatalf("build providers: %v", err)
	}
	only := map[string]auth.Provider{"google": provs["google"]}
	if only["google"] == nil {
		t.Fatalf("google provider not built; got %v", provs)
	}
	prev := fwdProvidersAtomic.Load()
	fwdProvidersAtomic.Store(&only)
	t.Cleanup(func() { fwdProvidersAtomic.Store(prev) })
}
