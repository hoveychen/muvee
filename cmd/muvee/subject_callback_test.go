package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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

func (p *fakeSubjectProvider) Name() string                 { return "faketwitter" }
func (p *fakeSubjectProvider) DisplayName() string          { return "Fake X" }
func (p *fakeSubjectProvider) OrgScoped() bool              { return false }
func (p *fakeSubjectProvider) CanonicalRedirectURL() string { return "https://example.com/_oauth/faketwitter" }
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

	const state = "state-nonce-for-pkce"
	r := httptest.NewRequest(http.MethodGet, "/_oauth/faketwitter?code=authcode&state="+state, nil)
	r.Header.Set("X-Forwarded-Host", "myproj.example.com")
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_state", Value: state})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "faketwitter")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handleOAuthCallback(w, r)

	if p.userInfoCalls != 0 {
		t.Errorf("callback used the email-only UserInfo path %d time(s); a SubjectProvider must go through UserInfoWithSubject", p.userInfoCalls)
	}
	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d (body=%s)", w.Code, http.StatusFound, w.Body.String())
	}
	if p.gotState != state {
		t.Errorf("state passed to UserInfoWithSubject:\n  got:  %q\n  want: %q\n(PKCE providers recompute code_verifier from it)", p.gotState, state)
	}
	if want := "https://example.com/_oauth/faketwitter"; p.gotRedirect != want {
		t.Errorf("redirect URL passed to UserInfoWithSubject:\n  got:  %q\n  want: %q", p.gotRedirect, want)
	}
	if _, ok := namedCookie(w, "muvee_fwd_session"); !ok {
		t.Error("callback must issue the muvee_fwd_session cookie for an email-less identity too")
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

func (p *fakePlainProvider) Name() string                 { return "fakeplain" }
func (p *fakePlainProvider) DisplayName() string          { return "Fake Plain" }
func (p *fakePlainProvider) OrgScoped() bool              { return false }
func (p *fakePlainProvider) CanonicalRedirectURL() string { return "https://example.com/_oauth/fakeplain" }
func (p *fakePlainProvider) AuthCodeURL(state, redirectURL string) string {
	return "https://idp.example/authorize?state=" + state
}
func (p *fakePlainProvider) UserInfo(ctx context.Context, code, redirectURL string) (string, string, string, error) {
	p.calls++
	return p.email, p.name, "", nil
}
