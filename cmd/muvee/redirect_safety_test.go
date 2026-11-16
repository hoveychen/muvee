package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		{"http://proj.opencws.top/", "proj.opencws.top", true},
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

// Logout must keep clearing the session exactly as before: same cookie name,
// Max-Age<0, scoped to the base domain the request arrived on.
func TestFwdLogout_ClearsSessionOnRequestBaseDomain(t *testing.T) {
	withRedirectDomains(t)
	for host, wantDomain := range map[string]string{
		"datamap.sigma-fi.com": "sigma-fi.com",
		"proj.opencws.top":     "opencws.top",
	} {
		res := doLogout(t, host, "?redirect=https://evil.com/")
		var got *http.Cookie
		for _, c := range res.Cookies() {
			if c.Name == "muvee_fwd_session" {
				got = c
			}
		}
		if got == nil || got.MaxAge >= 0 || got.Value != "" || got.Domain != wantDomain || got.Path != "/" || !got.HttpOnly {
			t.Fatalf("%s: session cookie not cleared as before: %+v", host, got)
		}
	}
}

// The post-login return URL arrives in a base-domain-wide cookie that any
// project subdomain (tenant code) can write. A tossed value pointing off the
// platform must not be followed after sign-in.
func TestPostLoginRedirect(t *testing.T) {
	withRedirectDomains(t)
	tests := []struct {
		name, cookie, want string
	}{
		{"no cookie", "", "/"},
		{"project page kept", "https://datamap.sigma-fi.com/deep?x=1", "https://datamap.sigma-fi.com/deep?x=1"},
		{"alias base kept", "https://proj.opencws.top/", "https://proj.opencws.top/"},
		{"tossed external refused", "https://evil.com/phish", "/"},
		{"tossed protocol-relative refused", "//evil.com", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/_oauth/google?code=x", nil)
			r.Host = "sigma-fi.com"
			r.Header.Set("X-Forwarded-Host", "sigma-fi.com")
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: tt.cookie})
			}
			if got := postLoginRedirect(r); got != tt.want {
				t.Fatalf("postLoginRedirect = %q, want %q", got, tt.want)
			}
		})
	}
}

// The password / SMS handlers must route their final redirect through
// postLoginRedirect. Drive the real SMS-less path: password login success.
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
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://evil.com/phish"})
	w := httptest.NewRecorder()
	handlePasswordLogin(w, r)
	res := w.Result()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || strings.Contains(loc, "evil.com") {
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
		for _, want := range []string{"Sigma DataMap", "#123456", `href="/_oauth/login"`} {
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

// A return URL planted by another subdomain must not block the project-root
// backfill (else the user would land on "/" of the auth host — the console).
func TestSeedLoginReturnURL_OverwritesTossedValue(t *testing.T) {
	withRedirectDomains(t)
	r := httptest.NewRequest("GET", "/_oauth/login", nil)
	r.Host = "datamap.sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://evil.com/"})
	w := httptest.NewRecorder()
	seedLoginReturnURL(w, r)
	for _, c := range w.Result().Cookies() {
		if c.Name == "fwd_oauth_redirect" && c.Value == "https://datamap.sigma-fi.com/" {
			return
		}
	}
	t.Fatalf("tossed fwd_oauth_redirect not replaced: %v", w.Result().Cookies())
}

// Drift guard: the fwd_oauth_redirect cookie is attacker-writable from any
// sibling subdomain, so its value may only be read inside the two vetted
// helpers. A new handler that reads it directly must go through
// postLoginRedirect instead — update this guard only alongside such a review.
func TestFwdOAuthRedirectCookieReadOnlyViaVettedHelpers(t *testing.T) {
	src, err := os.ReadFile("authservice.go")
	if err != nil {
		t.Fatal(err)
	}
	var readers []string
	fn := ""
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "func ") {
			fn = line
		}
		if strings.Contains(line, `.Cookie("fwd_oauth_redirect")`) {
			readers = append(readers, fn)
		}
	}
	allowed := map[string]bool{
		"func postLoginRedirect(r *http.Request) string {":                  true,
		"func seedLoginReturnURL(w http.ResponseWriter, r *http.Request) {": true,
	}
	for _, f := range readers {
		if !allowed[f] {
			t.Errorf("fwd_oauth_redirect read outside vetted helpers in %q; use postLoginRedirect", f)
		}
	}
	if len(readers) != 2 {
		t.Errorf("expected exactly 2 vetted reads, found %d: %v", len(readers), readers)
	}
}
