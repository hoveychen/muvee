package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEntraProvider mirrors the real Entra provider's distinguishing trait: it
// has no avatar URL to give, only the Microsoft Graph photo bytes inlined as a
// base64 data URI (Entra v2.0 ID tokens carry no `picture` claim). The 8 KB
// photo is the size internal/auth/entra.go's own comment cites as typical for
// the 96x96 image it requests.
type fakeEntraProvider struct {
	avatar string
}

func newFakeEntraProvider(photoBytes int) *fakeEntraProvider {
	return &fakeEntraProvider{
		avatar: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(make([]byte, photoBytes)),
	}
}

func (p *fakeEntraProvider) Name() string        { return "fakeentra" }
func (p *fakeEntraProvider) DisplayName() string { return "Fake Microsoft" }
func (p *fakeEntraProvider) OrgScoped() bool     { return true }
func (p *fakeEntraProvider) CanonicalRedirectURL() string {
	return "https://example.com/_oauth/fakeentra"
}
func (p *fakeEntraProvider) AuthCodeURL(state, redirectURL string) string {
	return "https://login.microsoftonline.test/authorize?state=" + state
}

func (p *fakeEntraProvider) UserInfo(ctx context.Context, code, redirectURL string) (string, string, string, error) {
	return "user@contoso.com", "Contoso User", p.avatar, nil
}

// TestEntraCallbackSessionCookieCarriesStoredURL is the end-to-end check on the
// materialisation round-trip: the provider hands the callback an 8 KB inlined
// photo, muvee-server answers the identity upsert with the URL it stored for
// those bytes, and the session cookie must carry that URL rather than the data
// URI. Before this path existed the same login produced a ~15 KB cookie, which
// every browser drops silently, leaving the subdomain in a login loop.
func TestEntraCallbackSessionCookieCarriesStoredURL(t *testing.T) {
	p := newFakeEntraProvider(8 << 10)
	installFakeProvider(t, p)

	const storedURL = "https://muveeai.com/api/public/avatars/avatar-1a2b3c4d5e6f7a8b.jpg"
	var sentAvatar string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		sentAvatar = body["avatar_url"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"33333333-3333-3333-3333-333333333333","avatar_url":"` + storedURL + `"}`))
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

	r, subsiteState, _ := centralCallback(t, "fakeentra")
	w := httptest.NewRecorder()
	handleOAuthCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d (body=%s)", w.Code, http.StatusFound, w.Body.String())
	}

	// The raw data URI still goes UP to the server — that is where the bytes
	// get written to disk. Only the way back down is a URL.
	if !strings.HasPrefix(sentAvatar, "data:image/jpeg;base64,") {
		t.Errorf("identity upsert avatar_url = %.40q..., want the inlined data URI so the server can store it", sentAvatar)
	}

	// -007: the callback issued the muvee_fwd_session cookie → it issues the
	// central __Host-muvee_sso, then the subsite exchange issues its own
	// __Host-muvee_fwd. Both cookies are measured against the browser limit
	// and both must carry the stored URL, not the data URI.
	sso, ssoC := ssoClaims(t, w)
	if got := len(sso.Name) + 1 + len(sso.Value); got > browserCookieLimit {
		t.Errorf("SSO cookie is %d bytes, over the %d byte browser limit", got, browserCookieLimit)
	}
	if ssoC.AvatarURL != storedURL {
		t.Errorf("SSO avatar_url = %q, want the stored URL %q", ssoC.AvatarURL, storedURL)
	}
	cookie, claims := exchangeAtSubsite(t, w, subsiteState)
	if got := len(cookie.Name) + 1 + len(cookie.Value); got > browserCookieLimit {
		t.Errorf("session cookie is %d bytes, over the %d byte browser limit", got, browserCookieLimit)
	}
	if claims.AvatarURL != storedURL {
		t.Errorf("session avatar_url = %q, want the stored URL %q", claims.AvatarURL, storedURL)
	}
}

// TestEntraCallbackKeepsProviderAvatarWhenServerReturnsNone covers a
// muvee-server that answers without an avatar_url — an older build, or one with
// no asset storage configured. The provider's own value is kept, and the
// cookie-size guard is what stops an oversized one from killing the session.
func TestEntraCallbackKeepsProviderAvatarWhenServerReturnsNone(t *testing.T) {
	p := newFakeEntraProvider(8 << 10)
	installFakeProvider(t, p)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"44444444-4444-4444-4444-444444444444"}`))
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

	r, subsiteState, _ := centralCallback(t, "fakeentra")
	w := httptest.NewRecorder()
	handleOAuthCallback(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d", w.Code, http.StatusFound)
	}
	// -007: asserted on the SSO cookie and on the subsite session the code
	// exchange yields (the callback no longer signs a subsite session itself).
	sso, ssoC := ssoClaims(t, w)
	// The guard dropped the oversized avatar, so the sessions still fit.
	if got := len(sso.Name) + 1 + len(sso.Value); got > browserCookieLimit {
		t.Errorf("SSO cookie is %d bytes, over the %d byte browser limit", got, browserCookieLimit)
	}
	if ssoC.AvatarURL != "" {
		t.Errorf("SSO avatar_url = %.40q..., want it dropped by the size guard", ssoC.AvatarURL)
	}
	cookie, claims := exchangeAtSubsite(t, w, subsiteState)
	if got := len(cookie.Name) + 1 + len(cookie.Value); got > browserCookieLimit {
		t.Errorf("session cookie is %d bytes, over the %d byte browser limit", got, browserCookieLimit)
	}
	if claims.AvatarURL != "" {
		t.Errorf("session avatar_url = %.40q..., want it dropped by the size guard", claims.AvatarURL)
	}
}
