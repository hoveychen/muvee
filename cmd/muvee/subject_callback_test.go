package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hoveychen/muvee/internal/auth"
)

// fakeSubjectProvider mirrors the shape of the real Twitter/X provider: its
// IdP surfaces no email, the PKCE verifier is derived from the OAuth state, so
// the email-only UserInfo entry point is a hard error and the callback MUST
// take the SubjectProvider path.
type fakeSubjectProvider struct {
	userInfoCalls int
	gotState      string
	gotRedirect   string
}

func (p *fakeSubjectProvider) Name() string        { return "faketwitter" }
func (p *fakeSubjectProvider) DisplayName() string { return "Fake X" }
func (p *fakeSubjectProvider) OrgScoped() bool     { return false }
func (p *fakeSubjectProvider) CanonicalRedirectURL() string {
	return "https://example.com/_oauth/faketwitter"
}
func (p *fakeSubjectProvider) AuthCodeURL(state, redirectURL string) string {
	return "https://idp.example/authorize?state=" + state
}

func (p *fakeSubjectProvider) UserInfo(ctx context.Context, code, redirectURL string) (string, string, string, error) {
	p.userInfoCalls++
	return "", "", "", errAlwaysWrongPath
}

func (p *fakeSubjectProvider) UserInfoWithSubject(ctx context.Context, code, state, redirectURL string) (string, string, string, string, error) {
	p.gotState = state
	p.gotRedirect = redirectURL
	if state == "" {
		return "", "", "", "", errAlwaysWrongPath
	}
	return "sub-4242", "", "Fake User", "https://cdn.example/avatar.png", nil
}

var errAlwaysWrongPath = &wrongPathError{}

type wrongPathError struct{}

func (e *wrongPathError) Error() string {
	return "provider requires the SubjectProvider path; use UserInfoWithSubject with state"
}

// installFakeProvider swaps the live provider map for one holding p and
// restores the previous set afterwards.
func installFakeProvider(t *testing.T, p auth.Provider) {
	t.Helper()
	prev := fwdProvidersAtomic.Load()
	m := map[string]auth.Provider{p.Name(): p}
	fwdProvidersAtomic.Store(&m)
	t.Cleanup(func() { fwdProvidersAtomic.Store(prev) })
}

// -007: provider callbacks land on the CENTRAL host (here example.com, the
// forwardAuthBase host) with the sign-in context in the central host's own
// __Host-fwd_apex_state cookie and the provider state f1 = apexStateF1(nonce),
// and they no longer sign a subsite session: they sign the central
// __Host-muvee_sso and 302 to <subsite>/_oauth/session?code=&state=, where the
// subsite exchanges the code for its own audience-bound session.
const (
	callbackSubsiteHost = "myproj.example.com"
	callbackSubsiteAud  = "project:11111111-1111-1111-1111-111111111111"
	callbackCentralHost = "example.com"
)

// centralCallback builds the provider's redirect back to the central host for
// a login-mode sign-in started by a browser on callbackSubsiteHost. It returns
// the callback request, the subsite's __Host-fwd_state cookie (the browser
// still holds it) and the provider state (f1) the request carries.
func centralCallback(t *testing.T, provider string) (*http.Request, *http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	sr := httptest.NewRequest(http.MethodGet, "/_oauth/login", nil)
	sr.Host = callbackSubsiteHost
	nonce := writeSubsiteState(rec, sr, 0, false)
	subsite := findCookie(rec.Result().Cookies(), subsiteSlotName(nonce))
	if subsite == nil {
		t.Fatal("writeSubsiteState set no subsite state cookie")
	}

	st := apexState{Host: callbackSubsiteHost, Aud: callbackSubsiteAud, State: nonce, Nonce: randomToken(18), Mode: "login", Provider: provider}
	f1 := apexStateF1(st.Nonce)
	r := httptest.NewRequest(http.MethodGet, "/_oauth/"+provider+"?code=authcode&state="+url.QueryEscape(f1), nil)
	r.Host = callbackCentralHost
	r.AddCookie(&http.Cookie{Name: apexSlotName(f1), Value: sealValue("apex-state", st)})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", provider)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)), subsite, f1
}

// ssoClaims returns the claims of the central SSO session the callback set.
func ssoClaims(t *testing.T, w *httptest.ResponseRecorder) (*http.Cookie, *authClaims) {
	t.Helper()
	c, ok := namedCookie(w, fwdCookieName(ssoCookieBase))
	if !ok || c.Value == "" {
		t.Fatalf("callback issued no %s cookie (status %d body %q)", fwdCookieName(ssoCookieBase), w.Code, w.Body.String())
	}
	claims, err := parseFwdToken(c.Value, ssoAudience(callbackCentralHost), fwdKindSSO)
	if err != nil {
		t.Fatalf("parse SSO session: %v", err)
	}
	return c, claims
}

// exchangeAtSubsite follows the callback's 302 the way the browser does: the
// subsite's /_oauth/session with the code, the state and its own state cookie.
// It returns the subsite session cookie and its claims.
func exchangeAtSubsite(t *testing.T, w *httptest.ResponseRecorder, subsiteState *http.Cookie) (*http.Cookie, *authClaims) {
	t.Helper()
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || loc.Scheme != "https" || loc.Host != callbackSubsiteHost || loc.Path != "/_oauth/session" ||
		loc.Query().Get("code") == "" || loc.Query().Get("state") == "" {
		t.Fatalf("callback Location = %q, want https://%s/_oauth/session?code=&state=", w.Header().Get("Location"), callbackSubsiteHost)
	}
	r := httptest.NewRequest(http.MethodGet, loc.RequestURI(), nil)
	r.Host = callbackSubsiteHost
	r.Header.Set(routeAudienceHeader, callbackSubsiteAud)
	r.AddCookie(&http.Cookie{Name: subsiteState.Name, Value: subsiteState.Value})
	sw := httptest.NewRecorder()
	handleSSOSession(sw, r)
	if sw.Code != http.StatusFound {
		t.Fatalf("subsite code exchange: status %d body %q, want 302", sw.Code, sw.Body.String())
	}
	c, ok := namedCookie(sw, fwdCookieName(fwdSessionCookieBase))
	if !ok || c.Value == "" {
		t.Fatalf("subsite exchange issued no %s cookie", fwdCookieName(fwdSessionCookieBase))
	}
	claims, err := parseFwdToken(c.Value, callbackSubsiteAud, fwdKindSession)
	if err != nil {
		t.Fatalf("parse subsite session: %v", err)
	}
	return c, claims
}

// TestHandleOAuthCallback_SubjectProviderTakesSubjectPath is the regression
// test for the email-less downstream login: before the SubjectProvider
// dispatch existed, handleOAuthCallback called p.UserInfo unconditionally, so
// a provider like Twitter/X — which can only complete the exchange through
// UserInfoWithSubject, because its PKCE verifier is derived from the state —
// answered every callback with 500 "authentication failed".
func TestHandleOAuthCallback_SubjectProviderTakesSubjectPath(t *testing.T) {
	p := &fakeSubjectProvider{}
	installFakeProvider(t, p)

	var upsertBody map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &upsertBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"22222222-2222-2222-2222-222222222222"}`))
	}))
	t.Cleanup(upstream.Close)

	prevBase, prevCookie, prevServer, prevSecret := forwardAuthBase, cookieDomain, muveeServerURL, jwtSecret
	forwardAuthBase = "https://example.com"
	cookieDomain = "example.com"
	muveeServerURL = upstream.URL
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, muveeServerURL, jwtSecret = prevBase, prevCookie, prevServer, prevSecret
	})

	r, subsiteState, state := centralCallback(t, "faketwitter")

	w := httptest.NewRecorder()
	handleOAuthCallback(w, r)

	if p.userInfoCalls != 0 {
		t.Errorf("callback used the email-only UserInfo path %d time(s); a SubjectProvider must go through UserInfoWithSubject", p.userInfoCalls)
	}
	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d (body=%s)", w.Code, http.StatusFound, w.Body.String())
	}
	// -007: the provider state handed to UserInfoWithSubject was the signed
	// host-bound state → it is the central f1 state (apexStateF1(nonce)), which
	// is exactly what the provider echoed in the query.
	if p.gotState != state {
		t.Errorf("state passed to UserInfoWithSubject:\n  got:  %q\n  want: %q\n(PKCE providers recompute code_verifier from it)", p.gotState, state)
	}
	if want := "https://example.com/_oauth/faketwitter"; p.gotRedirect != want {
		t.Errorf("redirect URL passed to UserInfoWithSubject:\n  got:  %q\n  want: %q", p.gotRedirect, want)
	}
	// -007: the callback issued the muvee_fwd_session cookie for an email-less
	// identity → it issues the central SSO session (and the 302 hands a code to
	// the subsite, which then signs the host-only session): both must carry the
	// email-less identity.
	if _, sso := ssoClaims(t, w); sso.UserID != testUserID || sso.Email != "" || sso.Name != "Fake User" || sso.Provider != "faketwitter" {
		t.Errorf("SSO identity = %+v, want user_id %s, no email, name Fake User, provider faketwitter", sso, testUserID)
	}
	if _, sess := exchangeAtSubsite(t, w, subsiteState); sess.UserID != testUserID || sess.Email != "" || sess.Name != "Fake User" {
		t.Errorf("subsite session identity = %+v, want the same email-less identity", sess)
	}
	if upsertBody["provider_user_id"] != "sub-4242" {
		t.Errorf("identity upsert provider_user_id = %q, want %q — the users row can only be keyed on the subject", upsertBody["provider_user_id"], "sub-4242")
	}
	if upsertBody["provider"] != "faketwitter" {
		t.Errorf("identity upsert provider = %q, want %q", upsertBody["provider"], "faketwitter")
	}
	if upsertBody["email"] != "" {
		t.Errorf("identity upsert email = %q, want empty (the IdP surfaced none)", upsertBody["email"])
	}
}

// TestOAuthUserInfo_PlainProviderKeepsEmailPath pins the other half of the
// dispatch: a provider that does not implement SubjectProvider must keep using
// UserInfo and yield an empty subject, so the email-keyed upsert stays the
// default for Google / Feishu / WeCom / DingTalk.
func TestOAuthUserInfo_PlainProviderKeepsEmailPath(t *testing.T) {
	p := &fakePlainProvider{email: "alice@example.com", name: "Alice"}
	sub, email, name, _, err := oauthUserInfo(context.Background(), p, "code", "state", "https://example.com/_oauth/fakeplain")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub != "" {
		t.Errorf("sub = %q, want empty for a provider without SubjectProvider", sub)
	}
	if email != "alice@example.com" || name != "Alice" {
		t.Errorf("identity = (%q, %q), want (alice@example.com, Alice)", email, name)
	}
	if p.calls != 1 {
		t.Errorf("UserInfo called %d time(s), want 1", p.calls)
	}
}

type fakePlainProvider struct {
	email, name string
	calls       int
}

func (p *fakePlainProvider) Name() string        { return "fakeplain" }
func (p *fakePlainProvider) DisplayName() string { return "Fake Plain" }
func (p *fakePlainProvider) OrgScoped() bool     { return false }
func (p *fakePlainProvider) CanonicalRedirectURL() string {
	return "https://example.com/_oauth/fakeplain"
}
func (p *fakePlainProvider) AuthCodeURL(state, redirectURL string) string {
	return "https://idp.example/authorize?state=" + state
}
func (p *fakePlainProvider) UserInfo(ctx context.Context, code, redirectURL string) (string, string, string, error) {
	p.calls++
	return p.email, p.name, "", nil
}

// recordingUpstream stands in for muvee-server, recording the bodies posted to
// each internal auth endpoint so a test can assert which one the callback
// chose.
type recordingUpstream struct {
	hits map[string]map[string]string
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	t.Helper()
	u := &recordingUpstream{hits: map[string]map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]string{}
		_ = json.Unmarshal(raw, &body)
		u.hits[r.URL.Path] = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"22222222-2222-2222-2222-222222222222","granted":true}`))
	}))
	t.Cleanup(srv.Close)

	prevBase, prevCookie, prevServer, prevSecret := forwardAuthBase, cookieDomain, muveeServerURL, jwtSecret
	forwardAuthBase = "https://example.com"
	cookieDomain = "example.com"
	muveeServerURL = srv.URL
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, muveeServerURL, jwtSecret = prevBase, prevCookie, prevServer, prevSecret
	})
	return u
}

// -007: the invite token no longer rides a muvee_invite_token cookie
// (deleted) through the provider round trip. It stays in the return URL's
// query, so after sign-in the first /verify consumes it in place
// (handleVerify, X-Forwarded-Uri ?invite_token=). The two tests below keep
// the original intents — an email-less identity consumes the link against its
// user id via project-invite, an email identity through auth/upsert — but
// assert them at /verify; callback_doesNotConsume pins that the callback
// itself no longer touches the link, even for a leftover legacy cookie.

// inviteVerify drives handleVerify for an already signed-in subsite session
// opening the invitation link.
func inviteVerify(t *testing.T, id fwdIdentity, inviteToken string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := signSubsiteSession(id, callbackSubsiteAud, "sso-i", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/verify?project_id="+callbackSubsiteAud[len("project:"):], nil)
	r.Header.Set("X-Forwarded-Host", callbackSubsiteHost)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Uri", "/?invite_token="+inviteToken)
	r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
	w := httptest.NewRecorder()
	handleVerify(w, r)
	return w
}

// TestHandleVerify_EmailLessIdentityConsumesProjectInvite covers the
// invitation link for an identity with no email. The email-keyed auth/upsert
// cannot serve it — the invitee would silently lose the grant and land on
// request-access — so the link is consumed against the user id the session
// carries.
func TestHandleVerify_EmailLessIdentityConsumesProjectInvite(t *testing.T) {
	upstream := newRecordingUpstream(t)
	w := inviteVerify(t, fwdIdentity{UserID: testUserID, Name: "Fake User", Provider: "faketwitter"}, "INVITE-TOK")

	if w.Code == http.StatusBadGateway {
		t.Fatalf("verify failed upstream (body=%s)", w.Body.String())
	}
	invite, ok := upstream.hits["/api/internal/auth/project-invite"]
	if !ok {
		t.Fatalf("invite token was dropped: nothing posted to /api/internal/auth/project-invite; hits=%v", upstream.hits)
	}
	if invite["invite_token"] != "INVITE-TOK" {
		t.Errorf("project-invite invite_token = %q, want INVITE-TOK", invite["invite_token"])
	}
	if invite["user_id"] != testUserID {
		t.Errorf("project-invite user_id = %q, want %q", invite["user_id"], testUserID)
	}
	if _, ok := upstream.hits["/api/internal/auth/upsert"]; ok {
		t.Error("email-keyed auth/upsert must not be called for an email-less identity")
	}
}

// TestHandleVerify_EmailIdentityKeepsInviteUpsert guards the untouched path:
// an identity with an email still consumes its link through auth/upsert, which
// additionally applies the platform-side invite bookkeeping.
func TestHandleVerify_EmailIdentityKeepsInviteUpsert(t *testing.T) {
	upstream := newRecordingUpstream(t)
	w := inviteVerify(t, fwdIdentity{UserID: testUserID, Email: "alice@corp.example", Name: "Alice", Provider: "fakeplain"}, "INVITE-TOK")

	if w.Code == http.StatusBadGateway {
		t.Fatalf("verify failed upstream (body=%s)", w.Body.String())
	}
	upsert, ok := upstream.hits["/api/internal/auth/upsert"]
	if !ok {
		t.Fatalf("expected the email path to consume the link via auth/upsert; hits=%v", upstream.hits)
	}
	if upsert["invite_token"] != "INVITE-TOK" || upsert["email"] != "alice@corp.example" {
		t.Errorf("auth/upsert body = %v, want the email and the token", upsert)
	}
	if _, ok := upstream.hits["/api/internal/auth/project-invite"]; ok {
		t.Error("the email path must not also hit the user-id project-invite endpoint")
	}
}

// -007: the callback consumed a muvee_invite_token cookie → the callback never
// consumes an invite (the cookie is not read, not set); it only syncs the
// identity. A stale legacy invite cookie riding the callback changes nothing.
func TestHandleOAuthCallback_DoesNotConsumeInvite(t *testing.T) {
	for _, p := range []auth.Provider{&fakeSubjectProvider{}, &fakePlainProvider{email: "alice@corp.example", name: "Alice"}} {
		t.Run(p.Name(), func(t *testing.T) {
			upstream := newRecordingUpstream(t)
			installFakeProvider(t, p)
			r, _, _ := centralCallback(t, p.Name())
			r.AddCookie(&http.Cookie{Name: "muvee_invite_token", Value: "INVITE-TOK"})
			w := httptest.NewRecorder()
			handleOAuthCallback(w, r)

			if w.Code != http.StatusFound {
				t.Fatalf("callback status = %d, want 302 (body=%s)", w.Code, w.Body.String())
			}
			if _, ok := upstream.hits["/api/internal/auth/identity-upsert"]; !ok {
				t.Fatalf("expected the identity to be upserted; hits=%v", upstream.hits)
			}
			for _, path := range []string{"/api/internal/auth/upsert", "/api/internal/auth/project-invite"} {
				if _, ok := upstream.hits[path]; ok {
					t.Errorf("callback consumed an invite via %s; the link is consumed at /verify now (hits=%v)", path, upstream.hits)
				}
			}
			for _, c := range w.Result().Cookies() {
				if c.Value != "" && (c.Name == "muvee_invite_token" || c.Name == fwdCookieName("muvee_invite_token")) {
					t.Errorf("callback set an invite cookie: %+v", c)
				}
			}
		})
	}
}
