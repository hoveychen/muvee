package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
)

// BUG-20261117-002: login CSRF through base-domain state cookies.
//
// Any tenant subdomain (evil.sigma-fi.com) can write cookies with
// Domain=sigma-fi.com. If the OAuth callback on the central host trusts a state
// cookie a sibling can write, the tenant plants a state of its choosing in the
// victim's browser and sends the victim to
// https://sigma-fi.com/_oauth/<p>?state=<that state>&code=<attacker's code>:
// the victim ends up signed in as the attacker (classic login CSRF), or — with
// a device-flow state — the attacker's CLI ends up holding the victim's
// identity. Only cookies a sibling cannot write (`__Host-` prefixed, which
// browsers force host-only) may vouch for a callback.

// csrfEnv pins the two-base-domain deployment and a fake provider whose code
// exchange always succeeds.
func csrfEnv(t *testing.T) {
	t.Helper()
	prevBase, prevDomain, prevBases, prevSecret := forwardAuthBase, cookieDomain, cookieBaseDomains, jwtSecret
	forwardAuthBase = "https://opencws.top"
	cookieDomain = "opencws.top"
	cookieBaseDomains = []string{"opencws.top", "sigma-fi.com"}
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() {
		forwardAuthBase, cookieDomain, cookieBaseDomains, jwtSecret = prevBase, prevDomain, prevBases, prevSecret
	})
	installFakeProvider(t, &fakeSubjectProvider{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"33333333-3333-3333-3333-333333333333"}`))
	}))
	t.Cleanup(upstream.Close)
	prevServer := muveeServerURL
	muveeServerURL = upstream.URL
	t.Cleanup(func() { muveeServerURL = prevServer })
}

// plantedCallback is the victim's browser landing on the central callback with
// only the cookies a sibling tenant could have planted (Domain=sigma-fi.com).
func plantedCallback(state string, planted map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet,
		"/_oauth/faketwitter?code=attacker-code&state="+url.QueryEscape(state), nil)
	r.Host = "sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "sigma-fi.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	for name, value := range planted {
		if strings.HasPrefix(name, "__Host-") {
			panic("a sibling subdomain cannot plant __Host- cookies: " + name)
		}
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "faketwitter")
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// issuedCredential reports whether the response sets any cookie carrying a JWT
// this service signed — a subsite session, a central SSO session, whatever it
// is called in the version under test.
func issuedCredential(w *httptest.ResponseRecorder) (string, bool) {
	for _, c := range w.Result().Cookies() {
		if c.Value == "" || c.MaxAge < 0 {
			continue
		}
		tok, err := jwt.Parse(c.Value, func(*jwt.Token) (interface{}, error) { return jwtSecret, nil })
		if err == nil && tok.Valid {
			return c.Name, true
		}
	}
	return "", false
}

func TestBUG002_PlantedLegacyStateDoesNotSignIn(t *testing.T) {
	csrfEnv(t)
	legacy := fmt.Sprintf("%d", time.Now().UnixNano())
	w := httptest.NewRecorder()
	handleOAuthCallback(w, plantedCallback(legacy, map[string]string{"fwd_oauth_state": legacy}))
	if name, ok := issuedCredential(w); ok {
		t.Fatalf("planted legacy state signed the victim in as the attacker (cookie %s, status %d, Location %q)",
			name, w.Code, w.Header().Get("Location"))
	}
}

func TestBUG002_PlantedSignedStateDoesNotSignIn(t *testing.T) {
	csrfEnv(t)
	// The attacker starts a real login of its own and harvests the state the
	// service minted for it, then replays it with the cookies it can plant.
	state, planted := attackerMintedState(t)
	w := httptest.NewRecorder()
	handleOAuthCallback(w, plantedCallback(state, planted))
	if name, ok := issuedCredential(w); ok {
		t.Fatalf("replayed attacker state signed the victim in as the attacker (cookie %s, status %d, Location %q)",
			name, w.Code, w.Header().Get("Location"))
	}
}

func TestBUG002_PlantedDeviceStateDoesNotHandOverVictim(t *testing.T) {
	csrfEnv(t)
	state, planted, completed := attackerDeviceState(t)
	w := httptest.NewRecorder()
	handleOAuthCallback(w, plantedCallback(state, planted))
	if completed() {
		t.Fatalf("planted device-flow state completed the attacker's device code with the victim's identity (status %d)", w.Code)
	}
}

// attackerMintedState: the attacker's own, legitimately minted login state plus
// the sibling-plantable cookies that accompany it.
func attackerMintedState(t *testing.T) (string, map[string]string) {
	t.Helper()
	s := signFwdState("evil.sigma-fi.com")
	return s, map[string]string{"fwd_oauth_state": s, fwdHostCookieName: "evil.sigma-fi.com"}
}

// attackerDeviceState: a device flow the attacker started from its own CLI.
// completed reports whether the victim's sign-in completed it.
func attackerDeviceState(t *testing.T) (string, map[string]string, func() bool) {
	t.Helper()
	deviceCode, userCode := "ATTACKERDEVICECODE", "ATKRCODE"
	entry := &deviceFlowEntry{UserCode: userCode, ExpiresAt: time.Now().Add(time.Minute)}
	deviceFlows.Store(deviceCode, entry)
	deviceByUser.Store(userCode, deviceCode)
	state := "device-" + newStateNonce()
	devicePending.Store(state, userCode)
	t.Cleanup(func() {
		deviceFlows.Delete(deviceCode)
		deviceByUser.Delete(userCode)
		devicePending.Delete(state)
	})
	return state, map[string]string{"fwd_oauth_state": state}, func() bool { return entry.Completed }
}
