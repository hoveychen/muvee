package main

import (
	"context"
	"fmt"
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
	if strings.ContainsAny(s, "&=+/ ") {
		t.Fatalf("state %q is not URL-safe", s)
	}
	if !verifyFwdState(s, host) {
		t.Fatal("fresh state rejected")
	}
	if verifyFwdState(s, "evil.sigma-fi.com") {
		t.Fatal("state accepted for a different host")
	}
	if signFwdState(host) == s {
		t.Fatal("two states for the same host are identical; nonce missing")
	}
	// Project hosts have no length limit; the state must stay inside WeCom's
	// 128-byte cap regardless.
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + ".sigma-fi.com"
	if n := len(signFwdState(long)); n > 128 {
		t.Fatalf("state for a %d-char host is %d bytes; WeCom caps state at 128", len(long), n)
	}
}

func TestFwdState_RejectsTamperedForeignAndExpired(t *testing.T) {
	withStateSecret(t)
	host := "datamap.sigma-fi.com"
	s := signFwdState(host)
	b := []byte(s)
	i := len(fwdStatePrefix) + 2
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	for _, v := range []string{string(b), "1763366400000000000", "device-123", "", s[:len(s)-2], s + "x"} {
		if verifyFwdState(v, host) {
			t.Errorf("verifyFwdState(%q) accepted", v)
		}
	}
	if verifyFwdState(s, "") {
		t.Error("state accepted without a host cookie")
	}
	jwtSecret = []byte("other-secret")
	if verifyFwdState(s, host) {
		t.Error("state signed with a different secret accepted")
	}
	jwtSecret = []byte("test-secret")
	old := signFwdStateAt(host, time.Now().Add(-time.Duration(stateMaxAge+5)*time.Second))
	if verifyFwdState(old, host) {
		t.Error("expired state accepted")
	}
	lt, _ := signState(stateClaims{Mode: "login-token", LoginToken: "x"})
	if verifyFwdState(lt, host) {
		t.Error("login-token state accepted as fwd state")
	}
}

func TestIsFreshLegacyState(t *testing.T) {
	now := time.Now()
	ok := []string{fmt.Sprintf("%d", now.UnixNano()), fmt.Sprintf("%d", now.Add(-4*time.Minute).UnixNano())}
	bad := []string{
		fmt.Sprintf("%d", now.Add(-11*time.Minute).UnixNano()),
		fmt.Sprintf("%d", now.Add(10*time.Minute).UnixNano()),
		"attacker-chosen", "state-nonce-for-pkce", "", "12345", "-1763366400000000000",
	}
	for _, v := range ok {
		if !isFreshLegacyState(v) {
			t.Errorf("isFreshLegacyState(%q) = false", v)
		}
	}
	for _, v := range bad {
		if isFreshLegacyState(v) {
			t.Errorf("isFreshLegacyState(%q) = true", v)
		}
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
	hc := findCookie(w.Result().Cookies(), fwdHostCookieName)
	if hc == nil || hc.Value != "datamap.sigma-fi.com" || hc.Domain != "sigma-fi.com" {
		t.Fatalf("host cookie %+v", hc)
	}
	if !verifyFwdState(state, hc.Value) {
		t.Fatalf("state %q not a signed state for the project host", state)
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
		{"non-platform bound host never used", "https://datamap.sigma-fi.com/", "sigma-fi.com", "evil.com", "/"},
		{"non-platform bound host keeps relative", "/x", "sigma-fi.com", "evil.com", "/x"},
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

func callbackRequest(state, cookieState, hostCookie, redirectCookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/_oauth/faketwitter?code=authcode&state="+url.QueryEscape(state), nil)
	r.Host = "sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "sigma-fi.com")
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_state", Value: cookieState})
	if hostCookie != "" {
		r.AddCookie(&http.Cookie{Name: fwdHostCookieName, Value: hostCookie})
	}
	if redirectCookie != "" {
		r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: redirectCookie})
	}
	r.AddCookie(&http.Cookie{Name: signedOutHintCookie, Value: "1"})
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
	handleOAuthCallback(w, callbackRequest(s, s, "datamap.sigma-fi.com", "https://evil.sigma-fi.com/phish"))
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "https://datamap.sigma-fi.com/" {
		t.Fatalf("Location = %q, want the starting project root", loc)
	}
	if c := findCookie(w.Result().Cookies(), signedOutHintCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("OAuth sign-in did not clear the signed-out hint: %+v", c)
	}
}

// Rewriting the (base-domain) host cookie to the tenant's own host breaks
// the hash check instead of redirecting the binding.
func TestOAuthCallback_SwappedHostCookieRejected(t *testing.T) {
	withFakeCallbackUpstream(t)
	s := signFwdState("datamap.sigma-fi.com")
	for _, hc := range []string{"evil.sigma-fi.com", ""} {
		w := httptest.NewRecorder()
		handleOAuthCallback(w, callbackRequest(s, s, hc, "https://evil.sigma-fi.com/phish"))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("host cookie %q: status=%d Location=%q, want 400", hc, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestOAuthCallback_LegacyStateOnlyWhileFresh(t *testing.T) {
	withFakeCallbackUpstream(t)
	fresh := fmt.Sprintf("%d", time.Now().UnixNano())
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(fresh, fresh, "", ""))
	if w.Code != http.StatusFound {
		t.Fatalf("fresh legacy state: status=%d, want 302", w.Code)
	}
	for _, st := range []string{fmt.Sprintf("%d", time.Now().Add(-11*time.Minute).UnixNano()), "attacker-chosen"} {
		w = httptest.NewRecorder()
		handleOAuthCallback(w, callbackRequest(st, st, "", "https://evil.sigma-fi.com/"))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("legacy state %q: status=%d, want 400", st, w.Code)
		}
	}
}

func TestOAuthCallback_StateCookieMismatchStillRejected(t *testing.T) {
	withFakeCallbackUpstream(t)
	a, b := signFwdState("datamap.sigma-fi.com"), signFwdState("datamap.sigma-fi.com")
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(a, b, "datamap.sigma-fi.com", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOAuthCallback_DeviceFlowState(t *testing.T) {
	withFakeCallbackUpstream(t)
	live := "device-" + newStateNonce()
	devicePending.Store(live, "NOSUCHCODE")
	t.Cleanup(func() { devicePending.Delete(live) })
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(live, live, "", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Device authorized") {
		t.Fatalf("live device state: status=%d", w.Code)
	}
	forged := "device-forged"
	w = httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(forged, forged, "", "https://evil.sigma-fi.com/"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("forged device state: status=%d, want 400", w.Code)
	}
}

func formLoginRequest(path, body string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Host = "datamap.sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://evil.sigma-fi.com/phish"})
	return r
}

// Password / SMS sign-ins run on the host the user is on; a sibling's tossed
// return URL must not move them elsewhere.
func TestFormLogins_RedirectBoundToCurrentHost(t *testing.T) {
	withRedirectDomains(t)
	withStateSecret(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/internal/projects/by-host") {
			_, _ = w.Write([]byte(`{"project_id":"p1","password_login":true,"sms_login":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"user_id":"u1","email":"u@example.com","phone":"+8613800000000","name":"u"}`))
	}))
	defer upstream.Close()
	prev := muveeServerURL
	muveeServerURL = upstream.URL
	t.Cleanup(func() { muveeServerURL = prev })

	for name, h := range map[string]struct {
		path, body string
		fn         http.HandlerFunc
	}{
		"password": {"/_oauth/password", "username=u&password=p", handlePasswordLogin},
		"sms":      {"/_oauth/sms/verify", "phone=%2B8613800000000&code=123456", handleSMSVerify},
	} {
		w := httptest.NewRecorder()
		h.fn(w, formLoginRequest(h.path, h.body))
		if findCookie(w.Result().Cookies(), "muvee_fwd_session") == nil {
			t.Fatalf("%s: no session issued (status %d)", name, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "https://datamap.sigma-fi.com/" {
			t.Fatalf("%s: Location=%q, want the current project root", name, loc)
		}
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
