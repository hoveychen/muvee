package main

// BACKLOG-20261121-001 X1: the per-host account page /_oauth/me. It shows
// which account this host is signed in as (its own session, the same one
// /_oauth/userinfo resolves), and offers sign-out, switch account and a link
// to the console. The central host shows the base domain's shared SSO sign-in.

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func accountPage(host, aud string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := epReq("GET", host, "/_oauth/me", nil)
	if aud != "" {
		r.Header.Set(routeAudienceHeader, aud)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleAccountPage(w, r)
	return w
}

func subsiteCookie(t *testing.T, id fwdIdentity, aud, ssoSID string) *http.Cookie {
	t.Helper()
	tok, err := signSubsiteSession(id, aud, ssoSID, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok}
}

func assertAccountHeaders(t *testing.T, label string, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("%s: Cache-Control = %q, want no-store", label, got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("%s: X-Frame-Options = %q, want SAMEORIGIN", label, got)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("%s: Content-Type = %q", label, got)
	}
}

// body is the unescaped page text, so assertions read like the rendered page.
func accountBody(w *httptest.ResponseRecorder) string { return html.UnescapeString(w.Body.String()) }

func TestAccountPage_SignedInShowsAccountAndActions(t *testing.T) {
	epEnv(t)
	id := epIdentity
	id.AvatarURL = "https://cdn.site-b.test/a.png"
	w := accountPage(epApp, epAud, subsiteCookie(t, id, epAud, "sso-me"))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	assertAccountHeaders(t, "signed in", w)
	body := accountBody(w)
	for _, want := range []string{
		id.Email, id.Name, id.AvatarURL,
		"Fake X",                // the sign-in method (provider display name)
		`href="/_oauth/logout"`, // same-origin sign-out (no confirmation page)
		"/_oauth/logout?redirect=" + url.QueryEscape("/_oauth/login?sso=none"), // switch account
		// console of the host's own base domain; _top because the console
		// refuses to be framed (BACKLOG-20261124-001) and this page may be.
		`href="https://site-b.test/settings/profile" target="_top"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("signed-in page lacks %q:\n%s", want, body)
		}
	}
	// The sign-out copy must say it signs out of the whole base domain.
	if !strings.Contains(body, "本域名") {
		t.Errorf("sign-out copy does not mention the base domain:\n%s", body)
	}
}

func TestAccountPage_EnglishCopy(t *testing.T) {
	epEnv(t)
	r := epReq("GET", epApp, "/_oauth/me", nil)
	r.Header.Set(routeAudienceHeader, epAud)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	r.AddCookie(subsiteCookie(t, epIdentity, epAud, "sso-en"))
	w := httptest.NewRecorder()
	handleAccountPage(w, r)
	body := accountBody(w)
	for _, want := range []string{"Signed in as", "Sign out", "Switch account", `lang="en"`} {
		if !strings.Contains(body, want) {
			t.Errorf("English page lacks %q", want)
		}
	}
}

func TestAccountPage_ProjectScopedPasswordSession(t *testing.T) {
	epEnv(t)
	tok, err := signProjectSession("demo", "Demo", "", "password", epProjectID)
	if err != nil {
		t.Fatal(err)
	}
	w := accountPage(epApp, epAud, &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
	body := accountBody(w)
	if w.Code != http.StatusOK || !strings.Contains(body, "demo") || !strings.Contains(body, "账号密码") {
		t.Fatalf("password session: status %d body %q", w.Code, body)
	}
}

func TestAccountPage_SignedOutShowsLogin(t *testing.T) {
	epEnv(t)
	for label, w := range map[string]*httptest.ResponseRecorder{
		"no cookie": accountPage(epApp, epAud),
		// A session of another site sent with this host's audience is not this
		// host's sign-in.
		"other audience": accountPage(epApp, epAud, subsiteCookie(t, epIdentity, "project:ep-proj-2", "sso-x")),
		// An SSO token is never a subsite credential.
		"sso as session": func() *httptest.ResponseRecorder {
			sso, _ := epSSOCookie(t)
			return accountPage(epApp, epAud, &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: sso.Value})
		}(),
	} {
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", label, w.Code)
		}
		assertAccountHeaders(t, label, w)
		body := accountBody(w)
		if strings.Contains(body, epIdentity.Email) || strings.Contains(body, epUserID) {
			t.Errorf("%s: identity shown while signed out", label)
		}
		if !strings.Contains(body, `href="/_oauth/login"`) || !strings.Contains(body, "未登录") {
			t.Errorf("%s: no signed-out state / login link:\n%s", label, body)
		}
		if strings.Contains(body, `href="/_oauth/logout"`) {
			t.Errorf("%s: sign-out offered while signed out", label)
		}
	}
}

func TestAccountPage_RevokedSessionIsSignedOutAndCleared(t *testing.T) {
	st := epEnv(t)
	cookie := subsiteCookie(t, epIdentity, epAud, "sso-me-rev")
	st.revokedSIDs["sso-me-rev"] = true
	w := accountPage(epApp, epAud, cookie)
	body := accountBody(w)
	if w.Code != http.StatusOK || strings.Contains(body, epIdentity.Email) || !strings.Contains(body, "未登录") {
		t.Fatalf("revoked: status %d body %q", w.Code, body)
	}
	set := strings.Join(w.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(set, fwdCookieName(fwdSessionCookieBase)+"=;") || !strings.Contains(set, fwdCookieName(signedOutHintCookie)+"=1") {
		t.Errorf("revoked session not cleared with the signed-out hint: %s", set)
	}
}

func TestAccountPage_RevocationCheckFailureFailsClosed(t *testing.T) {
	st := epEnv(t)
	st.checkFail = true
	w := accountPage(epApp, epAud, subsiteCookie(t, epIdentity, epAud, "sso-me-502"))
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), epIdentity.Email) {
		t.Fatalf("check failure: status %d body %q", w.Code, w.Body.String())
	}
	assertAccountHeaders(t, "502", w)
}

func TestAccountPage_CentralHostShowsSharedSSOOnly(t *testing.T) {
	epEnv(t)
	sub := subsiteCookie(t, epIdentity, epAud, "sso-c")
	// A forged audience header with a subsite cookie yields nothing here.
	w := accountPage(epCentral, epAud, sub)
	body := accountBody(w)
	if w.Code != http.StatusOK || strings.Contains(body, epIdentity.Email) {
		t.Fatalf("central forged aud: status %d body %q", w.Code, body)
	}
	assertAccountHeaders(t, "central", w)
	// The central host serves no login page: no /_oauth/login link there.
	if strings.Contains(body, `href="/_oauth/login`) {
		t.Errorf("central host links its (non-existent) login page:\n%s", body)
	}

	// The base domain's shared sign-in (SSO) is shown, and signing out of it
	// comes back to this page.
	sso, _ := epSSOCookie(t)
	w = accountPage(epCentral, "", sso)
	body = accountBody(w)
	if w.Code != http.StatusOK || !strings.Contains(body, epIdentity.Email) {
		t.Fatalf("central SSO: status %d body %q", w.Code, body)
	}
	if !strings.Contains(body, "/_oauth/logout?redirect="+url.QueryEscape("/_oauth/me")) {
		t.Errorf("central sign-out does not return to /_oauth/me:\n%s", body)
	}
	if strings.Contains(body, "sso%3Dnone") {
		t.Errorf("central host offers switch account (its login page does not exist):\n%s", body)
	}
}

func TestAccountPage_PlatformPagesLinkToIt(t *testing.T) {
	epEnv(t)
	// Logged-out page.
	r := epReq("GET", epApp, "/_oauth/logged-out", nil)
	w := httptest.NewRecorder()
	handleLoggedOutPage(w, r)
	if !strings.Contains(w.Body.String(), `href="/_oauth/me"`) {
		t.Errorf("logged-out page lacks the account page link")
	}
	// Request-access page (signed in).
	r = epReq("GET", epApp, "/_oauth/request-access", url.Values{"project": {epProjectID}})
	r.Header.Set(routeAudienceHeader, epAud)
	r.AddCookie(subsiteCookie(t, epIdentity, epAud, "sso-ra"))
	w = httptest.NewRecorder()
	handleRequestAccessPage(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `href="/_oauth/me"`) {
		t.Errorf("request-access page lacks the account page link: %d %q", w.Code, w.Body.String())
	}
}

// The route must be registered in the authservice router (which is built
// inline in runAuthService, so assert on the source like the other drift
// guards do).
func TestAccountPage_RouteRegistered(t *testing.T) {
	src, err := os.ReadFile("authservice.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `r.Get("/_oauth/me", handleAccountPage)`) {
		t.Fatal(`authservice router does not register GET /_oauth/me`)
	}
}
