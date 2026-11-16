package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func withStateSecret(t *testing.T) {
	t.Helper()
	prev := jwtSecret
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() { jwtSecret = prev })
}

func TestFwdState_RoundTrip(t *testing.T) {
	withStateSecret(t)
	host := "sigmafi-datamap.sigma-fi.com"
	s := signFwdState(host)
	if len(s) > 128 {
		t.Fatalf("state is %d bytes; WeCom caps state at 128", len(s))
	}
	if strings.ContainsAny(s, "&=+/ ") {
		t.Fatalf("state %q is not URL-safe", s)
	}
	got, ok := verifyFwdState(s)
	if !ok || got != host {
		t.Fatalf("verifyFwdState = %q,%v want %q,true", got, ok, host)
	}
	if signFwdState(host) == s {
		t.Fatal("two states for the same host are identical; nonce missing")
	}
}

func TestFwdState_RejectsTamperedForeignAndExpired(t *testing.T) {
	withStateSecret(t)
	s := signFwdState("datamap.sigma-fi.com")
	// Flip a payload character.
	b := []byte(s)
	i := strings.Index(s, ".") + 2
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	bad := []string{
		string(b),
		"1763366400000000000", // legacy numeric
		"device-123",          // device flow
		"",                    // empty
		s[:len(s)-2],          // truncated sig
	}
	for _, v := range bad {
		if _, ok := verifyFwdState(v); ok {
			t.Errorf("verifyFwdState(%q) accepted", v)
		}
	}
	// A state signed with another secret.
	jwtSecret = []byte("other-secret")
	if _, ok := verifyFwdState(s); ok {
		t.Error("state signed with a different secret accepted")
	}
	jwtSecret = []byte("test-secret")
	// Expired.
	old := signFwdStateAt("datamap.sigma-fi.com", time.Now().Add(-time.Duration(stateMaxAge+5)*time.Second))
	if _, ok := verifyFwdState(old); ok {
		t.Error("expired state accepted")
	}
	// A login-token style JSON state does not verify as a fwd state.
	lt, _ := signState(stateClaims{Mode: "login-token", LoginToken: "x"})
	if _, ok := verifyFwdState(lt); ok {
		t.Error("login-token state accepted as fwd state")
	}
}

// The provider-start leg must hand out an unpredictable, host-bound state.
func TestLoginPage_ProviderStartUsesSignedState(t *testing.T) {
	withSingleGoogleProject(t)
	withStateSecret(t)
	r := httptest.NewRequest("GET", "/_oauth/login?provider=google", nil)
	r.Host = "datamap.sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handleLoginPage(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d", w.Code)
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	host, ok := verifyFwdState(state)
	if !ok || host != "datamap.sigma-fi.com" {
		t.Fatalf("state %q not a signed state for the project host (host=%q ok=%v)", state, host, ok)
	}
	c := findCookie(w.Result().Cookies(), "fwd_oauth_state")
	if c == nil || c.Value != state {
		t.Fatalf("fwd_oauth_state cookie %+v does not match state", c)
	}
}

func TestBoundPostLoginRedirect(t *testing.T) {
	withRedirectDomains(t)
	tests := []struct {
		name, cookie, callbackHost, bound, want string
	}{
		{"same project kept", "https://datamap.sigma-fi.com/deep?x=1", "sigma-fi.com", "datamap.sigma-fi.com", "https://datamap.sigma-fi.com/deep?x=1"},
		{"tossed sibling refused", "https://evil.sigma-fi.com/phish", "sigma-fi.com", "datamap.sigma-fi.com", "https://datamap.sigma-fi.com/"},
		{"tossed alias base refused", "https://x.opencws.top/", "sigma-fi.com", "datamap.sigma-fi.com", "https://datamap.sigma-fi.com/"},
		{"no cookie lands on project root", "", "sigma-fi.com", "datamap.sigma-fi.com", "https://datamap.sigma-fi.com/"},
		{"apex login keeps relative", "", "opencws.top", "opencws.top", "/"},
		{"apex login keeps own path", "/projects", "opencws.top", "opencws.top", "/projects"},
		{"relative on project rebased", "/after", "sigma-fi.com", "datamap.sigma-fi.com", "https://datamap.sigma-fi.com/after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/_oauth/google?code=x", nil)
			r.Host = tt.callbackHost
			r.Header.Set("X-Forwarded-Host", tt.callbackHost)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: tt.cookie})
			}
			if got := boundPostLoginRedirect(r, tt.bound); got != tt.want {
				t.Fatalf("boundPostLoginRedirect = %q, want %q", got, tt.want)
			}
		})
	}
}

func callbackRequest(state, cookieState, redirectCookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/_oauth/faketwitter?code=authcode&state="+url.QueryEscape(state), nil)
	r.Host = "sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "sigma-fi.com")
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_state", Value: cookieState})
	if redirectCookie != "" {
		r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: redirectCookie})
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "faketwitter")
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withFakeCallbackUpstream(t *testing.T) {
	t.Helper()
	withRedirectDomains(t)
	withStateSecret(t)
	installFakeProvider(t, &fakeSubjectProvider{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"u1"}`))
	}))
	t.Cleanup(upstream.Close)
	prev := muveeServerURL
	muveeServerURL = upstream.URL
	t.Cleanup(func() { muveeServerURL = prev })
}

// A sibling tenant that tosses fwd_oauth_redirect at its own host cannot
// pull a user who started signing in on datamap over to it.
func TestOAuthCallback_RedirectBoundToStartingHost(t *testing.T) {
	withFakeCallbackUpstream(t)
	s := signFwdState("datamap.sigma-fi.com")
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(s, s, "https://evil.sigma-fi.com/phish"))
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "https://datamap.sigma-fi.com/" {
		t.Fatalf("Location = %q, want the starting project root", loc)
	}
}

func TestOAuthCallback_LegacyStateOnlyDuringUpgradeWindow(t *testing.T) {
	withFakeCallbackUpstream(t)
	prev := legacyStateDeadline
	t.Cleanup(func() { legacyStateDeadline = prev })

	legacyStateDeadline = time.Now().Add(time.Minute)
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest("1763366400000000000", "1763366400000000000", ""))
	if w.Code != http.StatusFound {
		t.Fatalf("in-window legacy state: status=%d, want 302", w.Code)
	}

	legacyStateDeadline = time.Now().Add(-time.Minute)
	w = httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest("1763366400000000000", "1763366400000000000", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("post-window legacy state: status=%d, want 400", w.Code)
	}
}

func TestOAuthCallback_StateCookieMismatchStillRejected(t *testing.T) {
	withFakeCallbackUpstream(t)
	a, b := signFwdState("datamap.sigma-fi.com"), signFwdState("datamap.sigma-fi.com")
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(a, b, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// Device-flow states are no longer a guessable timestamp.
func TestDeviceOAuthStateUnpredictable(t *testing.T) {
	src := mustRead(t, "authservice.go")
	if strings.Contains(src, `fmt.Sprintf("device-%d", time.Now().UnixNano())`) {
		t.Fatal("device flow state is still a timestamp")
	}
	if strings.Contains(src, `state := fmt.Sprintf("%d", time.Now().UnixNano())`) {
		t.Fatal("login state is still a timestamp")
	}
}

func mustRead(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
