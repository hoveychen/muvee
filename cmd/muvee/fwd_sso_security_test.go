package main

// BACKLOG-20261117-007 P7: security-focused tests covering blind spots an
// independent review found in the isolation design's test suite:
//   1. a login-token round trip must never issue a credential,
//   2. SSO sliding renewal keeps auth_time / sid (so console logout and
//      sign-out still void a renewed SSO),
//   3. /verify-admin only accepts host:traefik.<served base> sessions,
//   4. a Bearer minted for project B is useless against project A,
//   5. production can never drop the __Host- cookie prefix.
//
// Helpers are prefixed sec; they reuse the ep* / csrfEnv / revokeEnv helpers.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ─── 1. login-token flow issues no credential ───────────────────────────────

func TestSec_LoginTokenCallbackIssuesNoCredential(t *testing.T) {
	epEnv(t)

	// Create: the SDK asks the subsite host for a login token.
	cr := epReq(http.MethodPost, epApp, "/_oauth/login-token", nil)
	cr.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"provider":"faketwitter"}`)).Body
	cw := httptest.NewRecorder()
	handleLoginTokenCreate(cw, cr)
	if cw.Code != http.StatusOK {
		t.Fatalf("login-token create: status %d body %q", cw.Code, cw.Body.String())
	}
	var created struct {
		LoginToken string `json:"login_token"`
		OAuthURL   string `json:"oauth_url"`
	}
	if err := json.Unmarshal(cw.Body.Bytes(), &created); err != nil || created.LoginToken == "" || created.OAuthURL == "" {
		t.Fatalf("create response %q (err=%v)", cw.Body.String(), err)
	}
	t.Cleanup(func() { loginTokens.Delete(created.LoginToken) })
	u, err := url.Parse(created.OAuthURL)
	if err != nil || u.Query().Get("state") == "" {
		t.Fatalf("no state in oauth_url %q (err=%v)", created.OAuthURL, err)
	}
	state := u.Query().Get("state")

	// Callback: the provider round trip lands on the central host.
	w := httptest.NewRecorder()
	handleOAuthCallback(w, plantedCallback(state, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("login-token callback: status %d body %q (the flow should reach the completion page)", w.Code, w.Body.String())
	}
	if name, ok := issuedCredential(w); ok {
		t.Fatalf("login-token callback issued a credential: cookie %s", name)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("login-token callback set cookies: %v", w.Result().Cookies())
	}
	if loc := w.Header().Get("Location"); loc != "" || strings.Contains(loc, "code=") {
		t.Errorf("login-token callback redirected (Location %q), want a static page and no code hand-off", loc)
	}
	// Control: the entry did flip to success, so the poll below is
	// meaningful (not "expired").
	v, ok := loginTokens.Load(created.LoginToken)
	if !ok || v.(*loginTokenEntry).Status != "success" {
		t.Fatalf("control: login token entry not in success state after callback (ok=%v)", ok)
	}

	// Poll: status only, no Set-Cookie, no identity.
	pr := epReq(http.MethodPost, epApp, "/_oauth/login-token/poll", nil)
	pr.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"login_token":"`+created.LoginToken+`"}`)).Body
	pw := httptest.NewRecorder()
	handleLoginTokenPoll(pw, pr)
	if pw.Code != http.StatusOK {
		t.Fatalf("poll: status %d body %q", pw.Code, pw.Body.String())
	}
	if sc := pw.Header().Values("Set-Cookie"); len(sc) != 0 {
		t.Errorf("poll set cookies: %v", sc)
	}
	var body map[string]any
	if err := json.Unmarshal(pw.Body.Bytes(), &body); err != nil {
		t.Fatalf("poll body %q: %v", pw.Body.String(), err)
	}
	if len(body) != 1 || body["status"] != "success" {
		t.Errorf("poll body = %v, want exactly {status: success}", body)
	}
}

// ─── 2. SSO sliding renewal keeps auth_time and sid ─────────────────────────

// secOldSSO signs an SSO whose issue time is age ago with exp at expIn from
// now, carrying the given sid / auth_time.
func secOldSSO(t *testing.T, sid string, authTime time.Time, age, expIn time.Duration) *http.Cookie {
	t.Helper()
	c := identityClaims(epIdentity)
	c.Kind, c.SID, c.AuthTime = fwdKindSSO, sid, authTime.Unix()
	c.Subject = epIdentity.UserID
	c.Audience = jwt.ClaimStrings{ssoAudience(epCentral)}
	c.IssuedAt = jwt.NewNumericDate(time.Now().Add(-age))
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(expIn))
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: fwdCookieName(ssoCookieBase), Value: tok}
}

func TestSec_SSORenewalKeepsAuthTimeAndSID(t *testing.T) {
	st := epEnv(t)
	authTime := time.Now().Add(-3 * 24 * time.Hour).Truncate(time.Second)
	const sid = "sso-sid-renew"
	// Issued 3 days ago, 4 days of its 7 left: past the 1-day renew point.
	old := secOldSSO(t, sid, authTime, 3*24*time.Hour, 4*24*time.Hour)
	oldClaims, err := parseFwdToken(old.Value, ssoAudience(epCentral), fwdKindSSO)
	if err != nil {
		t.Fatalf("control: old SSO does not parse: %v", err)
	}

	w := epAuthorize(epApp, "s-renew", old)
	l := epLoc(t, w)
	if l.Query().Get("code") == "" {
		t.Fatalf("control: a live (old but valid) SSO did not mint a code: %s", l)
	}
	nc := findCookie(w.Result().Cookies(), fwdCookieName(ssoCookieBase))
	if nc == nil || nc.MaxAge < 0 || nc.Value == "" || nc.Value == old.Value {
		t.Fatalf("authorize did not write a renewed SSO cookie: %+v", nc)
	}
	renewed, err := parseFwdToken(nc.Value, ssoAudience(epCentral), fwdKindSSO)
	if err != nil {
		t.Fatalf("renewed SSO does not parse: %v", err)
	}
	if renewed.SID != sid {
		t.Errorf("renewed sid = %q, want %q (sign-out revokes by sid)", renewed.SID, sid)
	}
	if renewed.AuthTime != authTime.Unix() {
		t.Errorf("renewed auth_time = %d, want %d (console logout compares auth_time)", renewed.AuthTime, authTime.Unix())
	}
	if !renewed.ExpiresAt.Time.After(oldClaims.ExpiresAt.Time.Add(time.Hour)) {
		t.Errorf("renewed exp %v not extended past old exp %v", renewed.ExpiresAt.Time, oldClaims.ExpiresAt.Time)
	}
	if renewed.UserID != epIdentity.UserID || renewed.Provider != epIdentity.Provider {
		t.Errorf("renewed identity changed: %+v", renewed)
	}

	// Console logout after the original sign-in (but before the renewal): the
	// renewed SSO must still be void, i.e. renewal did not reset auth_time.
	st.consoleRevokedIAT = time.Now().Add(-24 * time.Hour).Unix()
	w2 := epAuthorize(epApp, "s-renew-2", &http.Cookie{Name: nc.Name, Value: nc.Value})
	l2 := epLoc(t, w2)
	if l2.Query().Get("sso") != "none" || l2.Query().Get("code") != "" {
		t.Fatalf("renewed SSO survived a console logout newer than its auth_time: %s", l2)
	}
	if c := findCookie(w2.Result().Cookies(), fwdCookieName(ssoCookieBase)); c == nil || c.MaxAge >= 0 {
		t.Errorf("revoked renewed SSO cookie not deleted: %+v", c)
	}
	// And a sid-level sign-out voids it too.
	st.consoleRevokedIAT = 0
	st.revokedSIDs[sid] = true
	l3 := epLoc(t, epAuthorize(epApp, "s-renew-3", &http.Cookie{Name: nc.Name, Value: nc.Value}))
	if l3.Query().Get("sso") != "none" || l3.Query().Get("code") != "" {
		t.Fatalf("renewed SSO survived a sign-out of its sid: %s", l3)
	}
}

func TestSec_SSOFreshNotRenewed(t *testing.T) {
	epEnv(t)
	fresh := secOldSSO(t, "sso-sid-fresh", time.Now(), time.Minute, ssoLifetime-time.Minute)
	w := epAuthorize(epApp, "s", fresh)
	if l := epLoc(t, w); l.Query().Get("code") == "" {
		t.Fatalf("control: fresh SSO did not mint a code: %s", l)
	}
	if c := findCookie(w.Result().Cookies(), fwdCookieName(ssoCookieBase)); c != nil {
		t.Errorf("a fresh SSO was re-signed (cookie written): %+v", c)
	}
}

// ─── 3. /verify-admin audience isolation ────────────────────────────────────

func TestSec_VerifyAdminAudienceIsolation(t *testing.T) {
	revokeEnv(t) // base domain example.com, nothing revoked
	adminEmails = map[string]struct{}{revokeIdentity.Email: {}}
	t.Cleanup(func() { adminEmails = nil })
	authTime := time.Now().Add(-time.Hour).Unix()

	req := func(xfh, aud string) *http.Request {
		tok, err := signSubsiteSession(revokeIdentity, aud, "sso-adm", authTime)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodGet, "/verify-admin", nil)
		r.Host = xfh
		r.Header.Set("X-Forwarded-Host", xfh)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
		return r
	}

	// Control: host:traefik.<base> session on the dashboard host passes.
	w := httptest.NewRecorder()
	handleVerifyAdmin(w, req("traefik.example.com", "host:traefik.example.com"))
	if w.Code != http.StatusOK {
		t.Fatalf("control: admin with a host:traefik.example.com session: status %d body %q", w.Code, w.Body.String())
	}

	cases := []struct{ name, xfh, aud string }{
		{"admin's project session", "traefik.example.com", "project:" + revokeProjectID},
		{"session of another base's dashboard", "traefik.example.com", "host:traefik.other.com"},
		{"session of a non-traefik host", "traefik.example.com", "host:app.example.com"},
		{"non-traefik forwarded host with its own host session", "app.example.com", "host:app.example.com"},
		{"non-traefik forwarded host with a traefik session", "app.example.com", "host:traefik.example.com"},
		{"traefik host of an unserved base", "traefik.other.com", "host:traefik.other.com"},
		{"no forwarded host", "", "host:traefik.example.com"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		handleVerifyAdmin(w, req(tc.xfh, tc.aud))
		if w.Code == http.StatusOK {
			t.Errorf("%s: admin accepted (status 200)", tc.name)
		}
		if got := w.Header().Get("X-Forwarded-User"); got != "" {
			t.Errorf("%s: identity headers set (%q) on a rejected request", tc.name, got)
		}
	}
}

// ─── 4. a Bearer for project B is useless against project A ─────────────────

func TestSec_CrossProjectBearerRejected(t *testing.T) {
	revokeEnv(t)
	const projA, projB = revokeProjectID, "22222222-aaaa-bbbb-cccc-222222222222"
	audA, audB := "project:"+projA, "project:"+projB
	authTime := time.Now().Add(-time.Hour).Unix()

	mk := func(kind, aud string) string {
		var tok string
		var err error
		if kind == "cli" {
			tok, err = signCLIToken(revokeIdentity, aud)
		} else {
			tok, err = signSubsiteSession(revokeIdentity, aud, "sso-x", authTime)
		}
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	verifyBearer := func(tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/verify?project_id="+projA, nil)
		r.Header.Set("X-Forwarded-Host", "myproj.example.com")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		handleVerify(w, r)
		return w
	}
	userinfoBearer := func(tok string) *httptest.ResponseRecorder {
		r := epReq(http.MethodGet, "myproj.example.com", "/_oauth/userinfo", nil)
		r.Header.Set(routeAudienceHeader, audA)
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		handleUserInfo(w, r)
		return w
	}

	for _, kind := range []string{"cli", "session"} {
		// Controls: project A's own token passes both endpoints.
		if w := verifyBearer(mk(kind, audA)); w.Code != http.StatusOK {
			t.Errorf("control: project A %s Bearer on /verify: status %d body %q", kind, w.Code, w.Body.String())
		}
		if w := userinfoBearer(mk(kind, audA)); w.Code != http.StatusOK {
			t.Errorf("control: project A %s Bearer on userinfo: status %d body %q", kind, w.Code, w.Body.String())
		}
		// Project B's token is rejected by both.
		if w := verifyBearer(mk(kind, audB)); w.Code == http.StatusOK {
			t.Errorf("project B %s Bearer accepted by project A /verify", kind)
		}
		w := userinfoBearer(mk(kind, audB))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("project B %s Bearer on project A userinfo: status %d, want 401", kind, w.Code)
		}
		if strings.Contains(w.Body.String(), revokeIdentity.Email) {
			t.Errorf("project B %s Bearer leaked identity from project A userinfo: %q", kind, w.Body.String())
		}
	}
}

// ─── 5. production cannot disable the __Host- prefix ────────────────────────

func TestSec_InsecureDevCookiesOnlyOnPlainHTTP(t *testing.T) {
	prevBase, prevDev := forwardAuthBase, devInsecureCookies
	t.Cleanup(func() {
		forwardAuthBase, devInsecureCookies = prevBase, prevDev
	})

	cases := []struct {
		name, base, env string
		wantDev         bool
		wantName        string
	}{
		{"https + FWD_INSECURE_DEV_COOKIES=1 (production)", "https://sigma-fi.com", "1", false, "__Host-muvee_fwd"},
		{"https, no variable", "https://sigma-fi.com", "", false, "__Host-muvee_fwd"},
		{"http + variable (local dev)", "http://localhost:8080", "1", true, fwdSessionCookieBase},
		{"http, no variable", "http://localhost:8080", "", false, "__Host-muvee_fwd"},
	}
	for _, tc := range cases {
		forwardAuthBase = tc.base
		t.Setenv("FWD_INSECURE_DEV_COOKIES", tc.env)
		initFwdCookieMode()
		if devInsecureCookies != tc.wantDev {
			t.Errorf("%s: devInsecureCookies = %v, want %v", tc.name, devInsecureCookies, tc.wantDev)
		}
		if got := fwdCookieName(fwdSessionCookieBase); got != tc.wantName {
			t.Errorf("%s: session cookie name = %q, want %q", tc.name, got, tc.wantName)
		}
		// A cookie written in production mode must carry Secure.
		w := httptest.NewRecorder()
		setHostCookie(w, fwdSessionCookieBase, "v", 60)
		c := findCookie(w.Result().Cookies(), tc.wantName)
		if c == nil {
			t.Errorf("%s: no cookie %q written", tc.name, tc.wantName)
		} else if c.Secure == tc.wantDev {
			t.Errorf("%s: Secure = %v, want %v", tc.name, c.Secure, !tc.wantDev)
		}
	}
}
