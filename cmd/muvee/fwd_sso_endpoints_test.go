package main

// BACKLOG-20261117-007 P7: endpoint-level tests of the central sign-in host and
// the subsite end of the isolation design: /_oauth/authorize, /_oauth/session,
// /_oauth/logout, request-access, the SDK popup page, routeAudience / userinfo.
//
// Everything here drives the real handlers against a muvee-server stub
// (epStub). Helpers are prefixed ep to stay clear of the other test files.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	epCentral   = "site-b.test"
	epApp       = "app.site-b.test"
	epTunnel    = "t1.site-b.test"
	epProjectID = "ep-proj-1"
	epAud       = "project:" + epProjectID
	epUserID    = "33333333-3333-3333-3333-333333333333"
)

type epCall struct {
	Path  string
	Query url.Values
	Body  string
	Key   string
}

// epStub stands in for muvee-server's internal API.
type epStub struct {
	mu                sync.Mutex
	hosts             map[string]string // by-host lookups: host -> JSON body
	revokedSIDs       map[string]bool
	consoleRevokedIAT int64 // >0: user credentials with session_iat <= this are revoked
	checkFail         bool
	revokeFail        bool
	calls             []epCall
}

func (s *epStub) callsTo(path string) []epCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []epCall
	for _, c := range s.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (s *epStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls = append(s.calls, epCall{r.URL.Path, r.URL.Query(), string(body), r.Header.Get("X-Muvee-Internal-Key")})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/internal/projects/by-host":
		s.mu.Lock()
		resp, ok := s.hosts[r.URL.Query().Get("host")]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(resp))
	case r.URL.Path == "/api/internal/auth/session-revoked":
		if s.checkFail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		q := r.URL.Query()
		s.mu.Lock()
		revoked := false
		for _, sid := range strings.Split(q.Get("sids"), ",") {
			if sid != "" && s.revokedSIDs[sid] {
				revoked = true
			}
		}
		if iat := q.Get("session_iat"); iat != "" && s.consoleRevokedIAT > 0 {
			var n int64
			for _, ch := range iat {
				n = n*10 + int64(ch-'0')
			}
			if n <= s.consoleRevokedIAT {
				revoked = true
			}
		}
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": revoked})
	case r.URL.Path == "/api/internal/auth/revoke-sids":
		if s.revokeFail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		var in struct {
			SIDs []string `json:"sids"`
		}
		_ = json.Unmarshal(body, &in)
		s.mu.Lock()
		for _, sid := range in.SIDs {
			s.revokedSIDs[sid] = true
		}
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	case r.URL.Path == "/api/internal/access/submit-request":
		_, _ = w.Write([]byte(`{"already_allowed":false}`))
	case strings.HasPrefix(r.URL.Path, "/api/internal/projects/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/internal/projects/")
		_, _ = w.Write([]byte(`{"id":"` + id + `","name":"EPProj"}`))
	default:
		_, _ = w.Write([]byte(`{"user_id":"` + epUserID + `"}`))
	}
}

func epProjectJSON(id, aud, enabled string) string {
	b, _ := json.Marshal(map[string]any{"project_id": id, "project_name": "app", "aud": aud, "enabled_providers": enabled})
	return string(b)
}

// epEnv pins two base domains (central hosts site-a.test and site-b.test), a
// fake provider "faketwitter" and the muvee-server stub.
func epEnv(t *testing.T) *epStub {
	t.Helper()
	csrfEnv(t)
	st := &epStub{
		revokedSIDs: map[string]bool{},
		hosts: map[string]string{
			epApp:                    epProjectJSON(epProjectID, epAud, ""),
			"onlyfeishu.site-b.test": epProjectJSON("ep-proj-2", "project:ep-proj-2", "feishu"),
			"nop.site-b.test":        epProjectJSON("ep-proj-3", "project:ep-proj-3", "none"),
			epTunnel:                 `{"tunnel":true,"aud":"host:` + epTunnel + `"}`,
			"badaud.site-b.test":     epProjectJSON("ep-proj-4", "project:someone-else", ""),
			"app.custom.com":         epProjectJSON("ep-proj-5", "project:ep-proj-5", ""), // known to the server, outside the base domains
			"app.site-a.test":        epProjectJSON("ep-proj-6", "project:ep-proj-6", ""), // other base domain
		},
	}
	srv := httptest.NewServer(st)
	t.Cleanup(srv.Close)
	prev := muveeServerURL
	muveeServerURL = srv.URL
	t.Cleanup(func() { muveeServerURL = prev })
	return st
}

func epReq(method, host, path string, q url.Values) *http.Request {
	target := path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	r := httptest.NewRequest(method, target, nil)
	r.Host = host
	r.Header.Set("X-Forwarded-Host", host)
	r.Header.Set("X-Forwarded-Proto", "https")
	return r
}

var epIdentity = fwdIdentity{UserID: epUserID, Email: "u@corp.example", Name: "U", Provider: "faketwitter"}

// epSSOCookie is a live central SSO session of the site-b.test central host.
func epSSOCookie(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	sid := "sso-" + randomToken(8)
	tok, err := signSSOSession(epIdentity, epCentral, sid, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: fwdCookieName(ssoCookieBase), Value: tok}, sid
}

func epAuthorize(host, state string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	q := url.Values{"host": {host}, "prompt": {"none"}}
	if state != "" {
		q.Set("state", state)
	}
	r := epReq("GET", epCentral, "/_oauth/authorize", q)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleSSOAuthorize(w, r)
	return w
}

func epLoc(t *testing.T, w *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Header().Get("Location") == "" {
		t.Fatalf("status %d: no usable Location (%q, err=%v); body %q", w.Code, w.Header().Get("Location"), err, w.Body.String())
	}
	return u
}

// ─── 1. central /_oauth/authorize ───────────────────────────────────────────

func TestEP_Authorize_LiveSSOMintsCodeBoundToTarget(t *testing.T) {
	epEnv(t)
	sso, ssoSID := epSSOCookie(t)
	for host, wantAud := range map[string]string{
		epApp:                 epAud,
		epTunnel:              "host:" + epTunnel,
		"traefik.site-b.test": "host:traefik.site-b.test",
	} {
		w := epAuthorize(host, "sub-state-"+host, sso)
		if w.Code != http.StatusFound {
			t.Fatalf("%s: status %d body %q", host, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", host, w.Header().Get("Cache-Control"))
		}
		loc := epLoc(t, w)
		if loc.Scheme+"://"+loc.Host+loc.Path != "https://"+host+"/_oauth/session" {
			t.Fatalf("%s: Location = %s, want the target's /_oauth/session", host, loc)
		}
		if loc.Query().Get("state") != "sub-state-"+host || loc.Query().Get("code") == "" {
			t.Fatalf("%s: Location query = %v", host, loc.Query())
		}
		c, ok := takeSSOCode(loc.Query().Get("code"))
		if !ok {
			t.Fatalf("%s: minted code not redeemable", host)
		}
		if c.Host != host || c.Aud != wantAud || c.SSOSID != ssoSID || c.StateDigest != stateDigest("sub-state-"+host) ||
			c.ID.Email != epIdentity.Email || c.ID.UserID != epUserID || c.ID.Provider != "faketwitter" || c.AuthTime == 0 {
			t.Fatalf("%s: code = %+v, want host/aud bound to the target and the SSO's sid", host, c)
		}
		if _, ok := takeSSOCode(loc.Query().Get("code")); ok {
			t.Errorf("%s: code redeemable twice", host)
		}
		if time.Until(c.Expires) > ssoCodeLifetime {
			t.Errorf("%s: code lives %v, more than %v", host, time.Until(c.Expires), ssoCodeLifetime)
		}
	}
}

func TestEP_Authorize_NoSSOPromptNoneBouncesBackWithSSONone(t *testing.T) {
	epEnv(t)
	w := epAuthorize(epApp, "sub-state-9")
	if w.Code != http.StatusFound {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	loc := epLoc(t, w)
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://"+epApp+"/_oauth/login" ||
		loc.Query().Get("sso") != "none" || loc.Query().Get("state") != "sub-state-9" || loc.Query().Get("code") != "" {
		t.Fatalf("Location = %s, want https://%s/_oauth/login?sso=none&state=sub-state-9 and no code", loc, epApp)
	}
	// A garbage / foreign-audience SSO cookie is the same as none (and is cleared).
	other, _ := signSSOSession(epIdentity, "site-a.test", "x", time.Now().Unix()) // SSO of the other central host
	w = epAuthorize(epApp, "s", &http.Cookie{Name: fwdCookieName(ssoCookieBase), Value: other})
	if l := epLoc(t, w); l.Query().Get("sso") != "none" || l.Query().Get("code") != "" {
		t.Fatalf("SSO of another central host accepted: %s", l)
	}
	if c := findCookie(w.Result().Cookies(), fwdCookieName(ssoCookieBase)); c == nil || c.MaxAge >= 0 {
		t.Errorf("invalid SSO cookie not cleared: %+v", c)
	}
	// A subsite session presented as the SSO cookie does not count either.
	sess, _ := signSubsiteSession(epIdentity, epAud, "x", time.Now().Unix())
	w = epAuthorize(epApp, "s", &http.Cookie{Name: fwdCookieName(ssoCookieBase), Value: sess})
	if l := epLoc(t, w); l.Query().Get("sso") != "none" || l.Query().Get("code") != "" {
		t.Fatalf("subsite session accepted as SSO: %s", l)
	}
}

func TestEP_Authorize_RevokedSSOTreatedAsNoSSO(t *testing.T) {
	for name, revoke := range map[string]func(*epStub, string){
		"sid revoked by a sign-out":  func(s *epStub, sid string) { s.revokedSIDs[sid] = true },
		"console logout after login": func(s *epStub, sid string) { s.consoleRevokedIAT = time.Now().Add(time.Minute).Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			st := epEnv(t)
			sso, sid := epSSOCookie(t)
			// Control: live SSO mints a code.
			if l := epLoc(t, epAuthorize(epApp, "s", sso)); l.Query().Get("code") == "" {
				t.Fatalf("control: live SSO did not mint a code: %s", l)
			}
			revoke(st, sid)
			w := epAuthorize(epApp, "s", sso)
			l := epLoc(t, w)
			if l.Query().Get("sso") != "none" || l.Query().Get("code") != "" {
				t.Fatalf("revoked SSO minted a code / not sso=none: %s", l)
			}
			if c := findCookie(w.Result().Cookies(), fwdCookieName(ssoCookieBase)); c == nil || c.MaxAge >= 0 || c.Value != "" {
				t.Errorf("revoked SSO cookie not deleted: %+v", c)
			}
			if len(st.callsTo("/api/internal/auth/session-revoked")) == 0 {
				t.Error("no session-revoked check was made")
			}
		})
	}
}

func TestEP_Authorize_RevocationCheckFailureFailsClosed(t *testing.T) {
	st := epEnv(t)
	sso, _ := epSSOCookie(t)
	st.checkFail = true
	w := epAuthorize(epApp, "s", sso)
	if w.Code != http.StatusBadGateway || w.Header().Get("Location") != "" {
		t.Fatalf("status %d Location %q, want 502 and no redirect (no code)", w.Code, w.Header().Get("Location"))
	}
}

func TestEP_Authorize_ProviderNotEnabledForProjectIsSSONone(t *testing.T) {
	epEnv(t)
	sso, _ := epSSOCookie(t) // provider faketwitter
	// Control: epApp inherits every loaded provider.
	if l := epLoc(t, epAuthorize(epApp, "s", sso)); l.Query().Get("code") == "" {
		t.Fatalf("control: allowed provider did not mint a code: %s", l)
	}
	for _, host := range []string{"onlyfeishu.site-b.test", "nop.site-b.test"} {
		w := epAuthorize(host, "s", sso)
		l := epLoc(t, w)
		if l.Host != host || l.Path != "/_oauth/login" || l.Query().Get("sso") != "none" || l.Query().Get("code") != "" {
			t.Fatalf("%s: Location = %s, want login?sso=none without code", host, l)
		}
		// The SSO itself stays (the user may still use another project).
		if c := findCookie(w.Result().Cookies(), fwdCookieName(ssoCookieBase)); c != nil && c.MaxAge < 0 {
			t.Errorf("%s: a still-valid SSO was deleted just because the project forbids its provider", host)
		}
	}
}

func TestEP_Authorize_RefusesNonSignInHosts(t *testing.T) {
	st := epEnv(t)
	sso, _ := epSSOCookie(t)
	cases := map[string]string{
		"outside every base domain (even if the server knows it)": "app.custom.com",
		"external host":                     "evil.com",
		"the base-domain apex itself":       "site-b.test",
		"the other base domain's apex":      "site-a.test",
		"a host of the other base domain":   "app.site-a.test",
		"unknown to by-host (404)":          "ghost.site-b.test",
		"by-host answers with a bad aud":    "badaud.site-b.test",
		"the central host with a subdomain": "x.y.site-b.test",
		"empty":                             "",
	}
	for label, host := range cases {
		for _, withSSO := range []bool{true, false} {
			var cookies []*http.Cookie
			if withSSO {
				cookies = append(cookies, sso)
			}
			w := epAuthorize(host, "s", cookies...)
			if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
				t.Errorf("%s (host %q, sso=%v): status %d Location %q, want 400 without Location", label, host, withSSO, w.Code, w.Header().Get("Location"))
			}
		}
	}
	// state is mandatory and bounded.
	for _, s := range []string{"", strings.Repeat("a", 129)} {
		w := epAuthorize(epApp, s, sso)
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Errorf("state len %d: status %d Location %q, want 400", len(s), w.Code, w.Header().Get("Location"))
		}
	}
	_ = st
}

func TestEP_Authorize_NotServedOnSubsiteHosts(t *testing.T) {
	epEnv(t)
	sso, _ := epSSOCookie(t)
	for _, host := range []string{epApp, "app.custom.com", "evil.com"} {
		r := epReq("GET", host, "/_oauth/authorize", url.Values{"host": {epApp}, "state": {"s"}, "prompt": {"none"}})
		r.AddCookie(sso)
		w := httptest.NewRecorder()
		handleSSOAuthorize(w, r)
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
			t.Errorf("%s: status %d Location %q, want 404 and no code", host, w.Code, w.Header().Get("Location"))
		}
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("%s: cookies set: %v", host, w.Result().Cookies())
		}
	}
}

// ─── 2. subsite /_oauth/session failure paths ───────────────────────────────

type epExch struct {
	Trips         int
	Popup         bool
	ReqHost       string // default epApp
	AudHeader     string // default epAud; "-" omits the header
	CodeHost      string // default epApp
	CodeAud       string // default epAud
	QueryState    string // default: the cookie nonce
	DigestOfState string // default: the cookie nonce
	NoStateCookie bool
	ExpireCode    bool
	Accept        string
}

func epSetDefault(p *string, d string) {
	if *p == "" {
		*p = d
	}
}

// epExchange stages a code exchange and returns a re-runnable request.
func epExchange(t *testing.T, o epExch) (run func() *httptest.ResponseRecorder, code string) {
	t.Helper()
	epSetDefault(&o.ReqHost, epApp)
	epSetDefault(&o.AudHeader, epAud)
	epSetDefault(&o.CodeHost, epApp)
	epSetDefault(&o.CodeAud, epAud)
	w0 := httptest.NewRecorder()
	nonce := writeSubsiteState(w0, subsiteReq(o.ReqHost), o.Trips, o.Popup)
	epSetDefault(&o.QueryState, nonce)
	epSetDefault(&o.DigestOfState, nonce)
	c := &ssoCode{
		Host: o.CodeHost, Aud: o.CodeAud, StateDigest: stateDigest(o.DigestOfState), SSOSID: "sso-ex", AuthTime: time.Now().Unix(), ID: epIdentity,
	}
	code = mintSSOCode(c)
	if o.ExpireCode {
		c.Expires = time.Now().Add(-time.Second)
	}
	return func() *httptest.ResponseRecorder {
		r := epReq("GET", o.ReqHost, "/_oauth/session", url.Values{"code": {code}, "state": {o.QueryState}})
		if o.AudHeader != "-" {
			r.Header.Set("X-Muvee-Aud", o.AudHeader)
		}
		if o.Accept != "" {
			r.Header.Set("Accept-Language", o.Accept)
		}
		if !o.NoStateCookie {
			carryCookies(w0, r)
		}
		w := httptest.NewRecorder()
		handleSSOSession(w, r)
		return w
	}, code
}

func epNoSession(t *testing.T, label string, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == fwdCookieName(fwdSessionCookieBase) && c.Value != "" && c.MaxAge >= 0 {
			t.Errorf("%s: a session cookie was issued: %+v", label, c)
		}
		if c.Name == fwdCookieName(ssoCookieBase) && c.Value != "" {
			t.Errorf("%s: an SSO cookie was issued on a subsite", label)
		}
	}
}

func TestEP_Session_ControlExchangeSignsOwnAudienceSession(t *testing.T) {
	withStateSecret(t)
	run, _ := epExchange(t, epExch{})
	w := run()
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatalf("status %d Location %q body %q", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	sess := findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase))
	if sess == nil || sess.Value == "" || sess.Domain != "" {
		t.Fatalf("session cookie %+v", sess)
	}
	c, err := parseFwdToken(sess.Value, epAud, fwdKindSession)
	if err != nil || c.SSOSID != "sso-ex" || c.Email != epIdentity.Email {
		t.Fatalf("session claims %+v err %v", c, err)
	}
	if _, err := parseFwdToken(sess.Value, "project:other", fwdKindSession); err == nil {
		t.Fatal("session valid for another audience")
	}
	if st := findCookie(w.Result().Cookies(), fwdCookieName(fwdStateCookieBase)); st == nil || st.MaxAge >= 0 {
		t.Errorf("state cookie not consumed: %+v", st)
	}
}

func TestEP_Session_FailuresSignNothingAndCountRetry(t *testing.T) {
	withStateSecret(t)
	cases := map[string]epExch{
		"expired code":                {ExpireCode: true},
		"X-Muvee-Aud missing":         {AudHeader: "-"},
		"X-Muvee-Aud of another site": {AudHeader: "project:other"},
		"code minted for another aud": {CodeAud: "project:other"},
		"query state != cookie nonce": {QueryState: "attacker-state", DigestOfState: "attacker-state"},
		"code bound to another state": {DigestOfState: "some-other-state"},
		"no __Host-fwd_state cookie":  {NoStateCookie: true},
		"code bound to another host":  {ReqHost: "evil.site-b.test", AudHeader: epAud}, // cookie + state valid on evil, code is for app
	}
	for label, o := range cases {
		t.Run(label, func(t *testing.T) {
			run, _ := epExchange(t, o)
			w := run()
			epNoSession(t, label, w)
			if w.Code != http.StatusFound {
				t.Fatalf("status %d, want 302 back to the login page", w.Code)
			}
			loc := epLoc(t, w)
			if loc.Path != "/_oauth/login" || loc.Host != "" || loc.Query().Get("attempt") != "1" {
				t.Fatalf("Location = %s, want /_oauth/login?attempt=1 (trips 0 -> 1)", loc)
			}
			if st := findCookie(w.Result().Cookies(), fwdCookieName(fwdStateCookieBase)); st == nil || st.MaxAge >= 0 {
				t.Errorf("state cookie not cleared on failure: %+v", st)
			}
		})
	}
}

func TestEP_Session_CodeIsSingleUse(t *testing.T) {
	withStateSecret(t)
	run, _ := epExchange(t, epExch{})
	if w := run(); w.Code != http.StatusFound || findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase)) == nil {
		t.Fatalf("first use failed: %d", w.Code)
	}
	// Replay with the very same URL and the very same (still held) state cookie.
	w := run()
	epNoSession(t, "replay", w)
	if loc := epLoc(t, w); loc.Path != "/_oauth/login" || loc.Query().Get("attempt") != "1" {
		t.Fatalf("replay Location = %s", loc)
	}
}

func TestEP_Session_FailedAttemptBurnsTheCode(t *testing.T) {
	withStateSecret(t)
	// First the wrong audience header (a router mix-up / forged header)...
	runBad, code := epExchange(t, epExch{AudHeader: "project:other"})
	epNoSession(t, "wrong aud", runBad())
	// ...then the right request with that same code must not work any more.
	if c, ok := takeSSOCode(code); ok {
		t.Fatalf("code survived a failed exchange: %+v", c)
	}
}

func TestEP_Session_RetryCounterAndErrorPage(t *testing.T) {
	withStateSecret(t)
	// trips 1 -> 2 still goes back to the login page, popup is kept.
	run, _ := epExchange(t, epExch{Trips: 1, Popup: true, AudHeader: "-"})
	w := run()
	loc := epLoc(t, w)
	if w.Code != http.StatusFound || loc.Path != "/_oauth/login" || loc.Query().Get("attempt") != "2" || loc.Query().Get("popup") != "1" {
		t.Fatalf("trips 1: status %d Location %s, want login?attempt=2&popup=1", w.Code, loc)
	}
	// trips 2 -> 3 > maxLoginRoundTrips: an error page, no more redirects.
	for _, o := range []epExch{{Trips: maxLoginRoundTrips, AudHeader: "-"}, {Trips: maxLoginRoundTrips, ExpireCode: true, Popup: true}} {
		o.Accept = "en"
		run, _ = epExchange(t, o)
		w = run()
		epNoSession(t, "over the limit", w)
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Fatalf("trips %d: status %d Location %q, want 400 error page without Location", o.Trips, w.Code, w.Header().Get("Location"))
		}
		if !strings.Contains(w.Body.String(), "Sign-in could not be completed") {
			t.Fatalf("error page body = %q", w.Body.String())
		}
		if st := findCookie(w.Result().Cookies(), fwdCookieName(fwdStateCookieBase)); st == nil || st.MaxAge >= 0 {
			t.Errorf("state cookie not cleared on the error page: %+v", st)
		}
	}
	// Without a readable state the counter restarts at 1 (no loop either).
	run, _ = epExchange(t, epExch{Trips: maxLoginRoundTrips, NoStateCookie: true})
	if loc := epLoc(t, run()); loc.Query().Get("attempt") != "1" {
		t.Fatalf("no state cookie: Location %s, want attempt=1", loc)
	}
}

func TestEP_LoginPage_AttemptOverLimitShowsErrorPage(t *testing.T) {
	epEnv(t)
	withStateSecret(t)
	for _, attempt := range []string{"3", "99", "-4"} {
		r := epReq("GET", epApp, "/_oauth/login", url.Values{"attempt": {attempt}})
		r.Header.Set("Accept-Language", "en")
		w := httptest.NewRecorder()
		handleLoginPage(w, r)
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "Sign-in could not be completed") {
			t.Errorf("attempt=%s: status %d Location %q body %q", attempt, w.Code, w.Header().Get("Location"), w.Body.String())
		}
	}
	// And the central host serves no login page at all.
	w := httptest.NewRecorder()
	handleLoginPage(w, epReq("GET", epCentral, "/_oauth/login", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("central host login page: status %d, want 400", w.Code)
	}
}

// ─── 3. /_oauth/logout ──────────────────────────────────────────────────────

func epLogoutReq(t *testing.T, sfs, query string) (*http.Request, string, string) {
	t.Helper()
	tok, err := signSubsiteSession(epIdentity, epAud, "sso-lo", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	r := epReq("GET", epApp, "/_oauth/logout"+query, nil)
	if sfs != "" {
		r.Header.Set("Sec-Fetch-Site", sfs)
	}
	r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
	return r, tokenForRevocation(tok).SID, "sso-lo"
}

func TestEP_Logout_NonSameOriginOnlyConfirms(t *testing.T) {
	st := epEnv(t)
	for _, sfs := range []string{"cross-site", "same-site", "none", "bogus"} {
		r, _, _ := epLogoutReq(t, sfs, "?redirect=/bye")
		r.Header.Set("Accept-Language", "en")
		w := httptest.NewRecorder()
		handleFwdLogout(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Location") != "" {
			t.Fatalf("%s: status %d Location %q, want the confirmation page", sfs, w.Code, w.Header().Get("Location"))
		}
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("%s: cookies changed by a cross-origin GET: %v", sfs, w.Result().Cookies())
		}
		if !strings.Contains(w.Body.String(), "Sign out?") || !strings.Contains(w.Body.String(), `href="/_oauth/logout?redirect=/bye"`) {
			t.Errorf("%s: confirm page lacks the confirm link: %q", sfs, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", sfs, w.Header().Get("Cache-Control"))
		}
	}
	if n := len(st.callsTo("/api/internal/auth/revoke-sids")); n != 0 {
		t.Fatalf("%d revoke call(s) made by cross-origin logout GETs", n)
	}
}

// The "" case pins a deliberate deviation from design §4 (which says "not
// same-origin → confirm"): requests without Sec-Fetch-Site (curl, browsers
// predating Fetch Metadata) sign out directly, so SDK signOut()'s fetch keeps
// working there. Pending confirmation by the coordinator; if §4 is applied
// literally, move "" into TestEP_Logout_NonSameOriginOnlyConfirms.
func TestEP_Logout_SameOriginRevokesBothSIDsAndClears(t *testing.T) {
	for _, sfs := range []string{"same-origin", ""} {
		t.Run("Sec-Fetch-Site="+sfs, func(t *testing.T) {
			st := epEnv(t)
			r, sid, ssoSID := epLogoutReq(t, sfs, "")
			w := httptest.NewRecorder()
			handleFwdLogout(w, r)
			if w.Code != http.StatusFound || w.Header().Get("Location") != loggedOutPath {
				t.Fatalf("status %d Location %q, want 302 %s", w.Code, w.Header().Get("Location"), loggedOutPath)
			}
			calls := st.callsTo("/api/internal/auth/revoke-sids")
			if len(calls) != 1 {
				t.Fatalf("%d revoke calls, want 1", len(calls))
			}
			var body struct {
				SIDs []string `json:"sids"`
			}
			if err := json.Unmarshal([]byte(calls[0].Body), &body); err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, s := range body.SIDs {
				got[s] = true
			}
			if sid == "" || !got[sid] || !got[ssoSID] || len(got) != 2 {
				t.Fatalf("revoked sids = %v, want exactly the session sid %q and sso_sid %q", body.SIDs, sid, ssoSID)
			}
			if calls[0].Key != internalKey {
				t.Errorf("revoke call internal key %q != %q", calls[0].Key, internalKey)
			}
			if c := findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase)); c == nil || c.MaxAge >= 0 || c.Value != "" || c.Domain != "" {
				t.Errorf("session cookie not cleared host-only: %+v", c)
			}
			if h := findCookie(w.Result().Cookies(), fwdCookieName(signedOutCookieBase)); h == nil || h.Value == "" || h.MaxAge <= 0 || h.Domain != "" {
				t.Errorf("signed-out hint not set host-only: %+v", h)
			}
			// The revocation is effective: the stub (like the server) now rejects both.
			if !st.revokedSIDs[sid] || !st.revokedSIDs[ssoSID] {
				t.Errorf("stub revoked set = %v", st.revokedSIDs)
			}
		})
	}
}

func TestEP_Logout_RedirectTargets(t *testing.T) {
	epEnv(t)
	for q, want := range map[string]string{
		"?redirect=/bye":                                          "/bye",
		"?redirect=https%3A%2F%2Fevil.com%2F":                     loggedOutPath,
		"?redirect=%2F%2Fevil.com":                                loggedOutPath,
		"?redirect=javascript%3Aalert(1)":                         loggedOutPath,
		"?redirect=https%3A%2F%2Fevil.com.site-b.test.evil.io%2F": loggedOutPath,
		"": loggedOutPath,
	} {
		r, _, _ := epLogoutReq(t, "same-origin", q)
		w := httptest.NewRecorder()
		handleFwdLogout(w, r)
		if w.Code != http.StatusFound || w.Header().Get("Location") != want {
			t.Errorf("%q: status %d Location %q, want %q", q, w.Code, w.Header().Get("Location"), want)
		}
	}
}

func TestEP_Logout_RevokeFailureStillClearsCookie(t *testing.T) {
	st := epEnv(t)
	st.revokeFail = true
	r, _, _ := epLogoutReq(t, "same-origin", "")
	w := httptest.NewRecorder()
	handleFwdLogout(w, r)
	if len(st.callsTo("/api/internal/auth/revoke-sids")) != 1 {
		t.Fatal("revoke endpoint not attempted")
	}
	if w.Code != http.StatusFound || w.Header().Get("Location") != loggedOutPath {
		t.Fatalf("status %d Location %q", w.Code, w.Header().Get("Location"))
	}
	if c := findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase)); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("session cookie not cleared after a failed revoke: %+v", c)
	}
	if h := findCookie(w.Result().Cookies(), fwdCookieName(signedOutCookieBase)); h == nil || h.MaxAge <= 0 {
		t.Errorf("signed-out hint missing after a failed revoke: %+v", h)
	}
}

func TestEP_Logout_WithoutSessionDoesNotCallRevoke(t *testing.T) {
	st := epEnv(t)
	r := epReq("GET", epApp, "/_oauth/logout", nil)
	w := httptest.NewRecorder()
	handleFwdLogout(w, r)
	if w.Code != http.StatusFound || len(st.callsTo("/api/internal/auth/revoke-sids")) != 0 {
		t.Fatalf("status %d, revoke calls %d", w.Code, len(st.callsTo("/api/internal/auth/revoke-sids")))
	}
	if c := findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase)); c == nil || c.MaxAge >= 0 {
		t.Errorf("session cookie not cleared: %+v", c)
	}
}

// ─── 4. request-access ──────────────────────────────────────────────────────

func epAccessPost(t *testing.T, host, hdrAud, sessAud, projectID string) *http.Request {
	t.Helper()
	r := epReq("POST", host, "/_oauth/request-access", nil)
	r.Body = io.NopCloser(strings.NewReader(url.Values{"project_id": {projectID}, "reason": {"please"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if hdrAud != "" {
		r.Header.Set("X-Muvee-Aud", hdrAud)
	}
	if sessAud != "" {
		tok, err := signSubsiteSession(epIdentity, sessAud, "sso-ra", time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
	}
	return r
}

func TestEP_RequestAccessSubmit_ProjectMustMatchSessionAudience(t *testing.T) {
	st := epEnv(t)
	submits := func() int { return len(st.callsTo("/api/internal/access/submit-request")) }

	// A session of project A asking for project B: refused, nothing sent upstream.
	w := httptest.NewRecorder()
	handleRequestAccessSubmit(w, epAccessPost(t, epApp, epAud, epAud, "ep-proj-OTHER"))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "another project") {
		t.Fatalf("foreign project_id: status %d body %q, want 400 'another project'", w.Code, w.Body.String())
	}
	if submits() != 0 {
		t.Fatal("a request for another project reached muvee-server")
	}

	// Own project: goes through to the server with the session's identity.
	w = httptest.NewRecorder()
	handleRequestAccessSubmit(w, epAccessPost(t, epApp, epAud, epAud, epProjectID))
	if w.Code != http.StatusOK {
		t.Fatalf("own project: status %d body %q", w.Code, w.Body.String())
	}
	calls := st.callsTo("/api/internal/access/submit-request")
	if len(calls) != 1 {
		t.Fatalf("%d submit calls, want 1", len(calls))
	}
	var sent map[string]string
	_ = json.Unmarshal([]byte(calls[0].Body), &sent)
	if sent["project_id"] != epProjectID || sent["email"] != epIdentity.Email || sent["user_id"] != epUserID || sent["reason"] != "please" {
		t.Fatalf("submit body = %v", sent)
	}
	if !strings.Contains(w.Body.String(), "EPProj") {
		t.Errorf("confirmation page does not name the project: %q", w.Body.String())
	}
}

func TestEP_RequestAccessSubmit_RequiresSessionOfTheRouteAudience(t *testing.T) {
	st := epEnv(t)
	cases := map[string]*http.Request{
		"session of another project's audience": epAccessPost(t, epApp, epAud, "project:ep-proj-OTHER", epProjectID),
		"no X-Muvee-Aud header":                 epAccessPost(t, epApp, "", epAud, epProjectID),
		"no session":                            epAccessPost(t, epApp, epAud, "", epProjectID),
		// On the central host the header is ignored, so no audience resolves.
		"forged X-Muvee-Aud on the central host": epAccessPost(t, epCentral, epAud, epAud, epProjectID),
	}
	for label, r := range cases {
		w := httptest.NewRecorder()
		handleRequestAccessSubmit(w, r)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/_oauth/login" {
			t.Errorf("%s: status %d Location %q, want 302 /_oauth/login", label, w.Code, w.Header().Get("Location"))
		}
	}
	if n := len(st.callsTo("/api/internal/access/submit-request")); n != 0 {
		t.Fatalf("%d submit call(s) from unauthenticated requests", n)
	}
}

func TestEP_RequestAccessPage_ForeignProjectRefused(t *testing.T) {
	epEnv(t)
	for project, wantStatus := range map[string]int{"ep-proj-OTHER": http.StatusBadRequest, epProjectID: http.StatusOK} {
		r := epReq("GET", epApp, "/_oauth/request-access", url.Values{"project": {project}})
		r.Header.Set("X-Muvee-Aud", epAud)
		tok, _ := signSubsiteSession(epIdentity, epAud, "sso-ra", time.Now().Unix())
		r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
		w := httptest.NewRecorder()
		handleRequestAccessPage(w, r)
		if w.Code != wantStatus {
			t.Errorf("project=%s: status %d, want %d (body %q)", project, w.Code, wantStatus, w.Body.String())
		}
	}
}

// ─── 5. popup ───────────────────────────────────────────────────────────────

func TestEP_PopupDone_PageContract(t *testing.T) {
	w := httptest.NewRecorder()
	handlePopupDone(w, epReq("GET", epApp, popupDonePath, nil))
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Content-Type %q Cache-Control %q", w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"))
	}
	if !regexp.MustCompile(`opener\.postMessage\(\s*msg\s*,\s*location\.origin\s*\)`).MatchString(body) {
		t.Errorf("page does not postMessage to the opener with targetOrigin location.origin:\n%s", body)
	}
	if strings.Contains(body, `'*'`) || strings.Contains(body, `"*"`) {
		t.Error("wildcard targetOrigin in the popup page")
	}
	if !strings.Contains(body, "new BroadcastChannel('muvee-auth')") || !strings.Contains(body, "'muvee-auth'") ||
		!strings.Contains(body, "status: 'signed-in'") {
		t.Errorf("page lacks the BroadcastChannel('muvee-auth') signed-in message:\n%s", body)
	}
	if !strings.Contains(body, "window.close()") {
		t.Error("page does not close itself")
	}
}

// epFullLogin walks the complete browser sign-in of a subsite through the three
// handlers and returns the final /_oauth/session response and the subsite jar.
func epFullLogin(t *testing.T, loginQuery url.Values) (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
	t.Helper()
	// 1. subsite login page, provider click.
	lw := httptest.NewRecorder()
	handleLoginPage(lw, epReq("GET", epApp, "/_oauth/login", loginQuery))
	if lw.Code != http.StatusFound {
		t.Fatalf("login: status %d body %q", lw.Code, lw.Body.String())
	}
	start := epLoc(t, lw)
	if start.Host != epCentral || start.Path != "/_oauth/start" || start.Query().Get("host") != epApp {
		t.Fatalf("login Location = %s, want the central /_oauth/start for %s", start, epApp)
	}
	// 2. the central host starts the provider.
	sr := epReq("GET", epCentral, "/_oauth/start", start.Query())
	sw := httptest.NewRecorder()
	handleSSOStart(sw, sr)
	if sw.Code != http.StatusFound {
		t.Fatalf("start: status %d body %q", sw.Code, sw.Body.String())
	}
	f1 := epLoc(t, sw).Query().Get("state")
	// 3. provider calls back on the central host.
	cr := epReq("GET", epCentral, "/_oauth/faketwitter", url.Values{"code": {"authcode"}, "state": {f1}})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "faketwitter")
	cr = cr.WithContext(context.WithValue(cr.Context(), chi.RouteCtxKey, rctx))
	carryCookies(sw, cr)
	cw := httptest.NewRecorder()
	handleOAuthCallback(cw, cr)
	if cw.Code != http.StatusFound {
		t.Fatalf("callback: status %d body %q", cw.Code, cw.Body.String())
	}
	back := epLoc(t, cw)
	if back.Host != epApp || back.Path != "/_oauth/session" {
		t.Fatalf("callback Location = %s", back)
	}
	if findCookie(cw.Result().Cookies(), fwdCookieName(ssoCookieBase)) == nil {
		t.Fatal("callback did not sign the central SSO")
	}
	// 4. the browser, back on the subsite, with its own jar (state cookie).
	xr := epReq("GET", epApp, "/_oauth/session", back.Query())
	xr.Header.Set("X-Muvee-Aud", epAud)
	carryCookies(lw, xr)
	xw := httptest.NewRecorder()
	handleSSOSession(xw, xr)
	return xw, cw
}

func TestEP_Popup_FullLoginEndsOnPopupDone(t *testing.T) {
	epEnv(t)
	xw, _ := epFullLogin(t, url.Values{"provider": {"faketwitter"}, "popup": {"1"}})
	if xw.Code != http.StatusFound || xw.Header().Get("Location") != popupDonePath {
		t.Fatalf("session: status %d Location %q, want 302 %s", xw.Code, xw.Header().Get("Location"), popupDonePath)
	}
	sess := findCookie(xw.Result().Cookies(), fwdCookieName(fwdSessionCookieBase))
	if sess == nil || sess.Value == "" {
		t.Fatal("popup login did not issue the subsite session")
	}
	c, err := parseFwdToken(sess.Value, epAud, fwdKindSession)
	if err != nil || c.UserID != epUserID || c.SSOSID == "" {
		t.Fatalf("session claims %+v err %v", c, err)
	}
}

func TestEP_Popup_ControlNonPopupGoesToReturnURLAndQueryCannotForcePopup(t *testing.T) {
	epEnv(t)
	xw, _ := epFullLogin(t, url.Values{"provider": {"faketwitter"}})
	if xw.Code != http.StatusFound || xw.Header().Get("Location") != "/" {
		t.Fatalf("status %d Location %q, want 302 /", xw.Code, xw.Header().Get("Location"))
	}
}

func TestEP_Popup_FlagLivesInStateNotInSessionQuery(t *testing.T) {
	withStateSecret(t)
	// A non-popup state: a forged &popup=1 on /_oauth/session must not matter.
	w0 := httptest.NewRecorder()
	nonce := writeSubsiteState(w0, subsiteReq(epApp), 0, false)
	code := mintSSOCode(&ssoCode{Host: epApp, Aud: epAud, StateDigest: stateDigest(nonce), SSOSID: "s", AuthTime: time.Now().Unix(), ID: epIdentity})
	r := epReq("GET", epApp, "/_oauth/session", url.Values{"code": {code}, "state": {nonce}, "popup": {"1"}})
	r.Header.Set("X-Muvee-Aud", epAud)
	carryCookies(w0, r)
	w := httptest.NewRecorder()
	handleSSOSession(w, r)
	if w.Header().Get("Location") != "/" {
		t.Fatalf("Location %q, want /", w.Header().Get("Location"))
	}
}

// ─── 6. routeAudience / userinfo ────────────────────────────────────────────

func TestEP_RouteAudience(t *testing.T) {
	epEnv(t)
	hdr := func(host, aud string, viaXFH bool) *http.Request {
		r := httptest.NewRequest("GET", "/_oauth/userinfo", nil)
		if viaXFH {
			r.Host = "traefik-internal"
			r.Header.Set("X-Forwarded-Host", host)
		} else {
			r.Host = host
		}
		if aud != "" {
			r.Header.Set("X-Muvee-Aud", aud)
		}
		return r
	}
	for _, viaXFH := range []bool{true, false} {
		// Central hosts: the header is ignored, even a forged one.
		for _, host := range []string{"site-b.test", "SITE-B.test", "site-a.test"} {
			if got := routeAudience(hdr(host, "project:victim", viaXFH)); got != "" {
				t.Errorf("central host %s (xfh=%v): routeAudience = %q, want empty", host, viaXFH, got)
			}
		}
		// Subsites: the injected value.
		for _, host := range []string{epApp, "app.site-a.test", epTunnel, "app.custom.com"} {
			if got := routeAudience(hdr(host, "project:p9", viaXFH)); got != "project:p9" {
				t.Errorf("subsite %s (xfh=%v): routeAudience = %q, want project:p9", host, viaXFH, got)
			}
			if got := routeAudience(hdr(host, "", viaXFH)); got != "" {
				t.Errorf("subsite %s without header: routeAudience = %q, want empty", host, got)
			}
		}
	}
	if got := routeAudience(hdr(epApp, "  host:x.site-b.test  ", true)); got != "host:x.site-b.test" {
		t.Errorf("header not trimmed: %q", got)
	}
}

func epUserinfo(host, hdrAud string, cookie *http.Cookie, bearer string) *httptest.ResponseRecorder {
	r := epReq("GET", host, "/_oauth/userinfo", nil)
	if hdrAud != "" {
		r.Header.Set("X-Muvee-Aud", hdrAud)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	handleUserInfo(w, r)
	return w
}

func TestEP_UserInfo_CentralHostNeverReturnsIdentity(t *testing.T) {
	epEnv(t)
	tok, err := signSubsiteSession(epIdentity, epAud, "sso-ui", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok}

	// Control: the same credential on its own subsite answers.
	w := epUserinfo(epApp, epAud, cookie, "")
	var info map[string]string
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &info) != nil || info["email"] != epIdentity.Email || info["user_id"] != epUserID {
		t.Fatalf("control: status %d body %q", w.Code, w.Body.String())
	}
	// Bearer works too on the subsite.
	if w := epUserinfo(epApp, epAud, nil, tok); w.Code != http.StatusOK {
		t.Fatalf("control bearer: status %d", w.Code)
	}

	// On the central host a forged audience header + a valid cookie / bearer yields nothing.
	for label, w := range map[string]*httptest.ResponseRecorder{
		"cookie":     epUserinfo(epCentral, epAud, cookie, ""),
		"bearer":     epUserinfo(epCentral, epAud, nil, tok),
		"other base": epUserinfo("site-a.test", epAud, cookie, ""),
	} {
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", label, w.Code)
		}
		if strings.Contains(w.Body.String(), epIdentity.Email) || strings.Contains(w.Body.String(), epUserID) {
			t.Errorf("%s: identity leaked on the central host: %q", label, w.Body.String())
		}
	}
	// Another site's audience header does not unlock this session either.
	if w := epUserinfo(epApp, "project:ep-proj-2", cookie, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong audience header: status %d, want 401", w.Code)
	}
	// An SSO token is never a subsite credential.
	sso, _ := epSSOCookie(t)
	if w := epUserinfo(epApp, epAud, &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: sso.Value}, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("SSO token as session cookie: status %d, want 401", w.Code)
	}
}

func TestEP_UserInfo_RevokedSessionRefused(t *testing.T) {
	st := epEnv(t)
	tok, _ := signSubsiteSession(epIdentity, epAud, "sso-rev", time.Now().Unix())
	cookie := &http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok}
	if w := epUserinfo(epApp, epAud, cookie, ""); w.Code != http.StatusOK {
		t.Fatalf("control: status %d", w.Code)
	}
	st.revokedSIDs["sso-rev"] = true // the sso_sid was revoked by a sign-out elsewhere
	w := epUserinfo(epApp, epAud, cookie, "")
	if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), epIdentity.Email) {
		t.Fatalf("revoked sso_sid: status %d body %q", w.Code, w.Body.String())
	}
}
