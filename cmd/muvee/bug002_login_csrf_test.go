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
// Any tenant subdomain (evil.site-b.test) can write cookies with
// Domain=site-b.test. If the OAuth callback on the central host trusts a state
// cookie a sibling can write, the tenant plants a state of its choosing in the
// victim's browser and sends the victim to
// https://site-b.test/_oauth/<p>?state=<that state>&code=<attacker's code>:
// the victim ends up signed in as the attacker (classic login CSRF), or — with
// a device-flow state — the attacker's CLI ends up holding the victim's
// identity. Only cookies a sibling cannot write (`__Host-` prefixed, which
// browsers force host-only) may vouch for a callback.

// csrfEnv pins the two-base-domain deployment and a fake provider whose code
// exchange always succeeds.
func csrfEnv(t *testing.T) {
	t.Helper()
	prevBase, prevDomain, prevBases, prevSecret := forwardAuthBase, cookieDomain, cookieBaseDomains, jwtSecret
	forwardAuthBase = "https://site-a.test"
	cookieDomain = "site-a.test"
	cookieBaseDomains = []string{"site-a.test", "site-b.test"}
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
// only the cookies a sibling tenant could have planted (Domain=site-b.test).
func plantedCallback(state string, planted map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet,
		"/_oauth/faketwitter?code=attacker-code&state="+url.QueryEscape(state), nil)
	r.Host = "site-b.test"
	r.Header.Set("X-Forwarded-Host", "site-b.test")
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

// -007: the attacker's state used to be minted with signFwdState / devicePending
// (both deleted); it now comes from the attacker's own legitimate browser
// request to the central host's /_oauth/start, which is the only way to obtain
// a valid provider state ("f1.<mac>") and its sealed __Host-fwd_apex_state
// cookie. The attacker can read both from its own browser, but a sibling
// subdomain can never plant a __Host- cookie in the victim's browser (the
// plantedCallback guard panics on one), so the strongest attack left is to
// replay the harvested f1 in the callback URL together with the cookie value
// under names a sibling CAN set (Domain=site-b.test): the unprefixed
// apexStateCookieBase, the unprefixed name of the per-sign-in slot (what the
// dev cookie mode would use) and the pre-upgrade fwd_oauth_state. The service
// must ignore them (it reads only the __Host- slot named after the f1).

const (
	attackerHost    = "evil.site-b.test"
	attackerProject = "99999999-9999-9999-9999-999999999999"
)

// attackerStart is the attacker's browser (its own cookie jar, Host
// site-b.test = the central host of that base domain) calling /_oauth/start
// for its own project host. It returns the provider state from the redirect
// and the __Host-fwd_apex_state cookie it was handed.
func attackerStart(t *testing.T, extra url.Values) (string, *http.Cookie) {
	t.Helper()
	// by-host lookup of the attacker's own (legitimate) project.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/internal/projects/by-host" && r.URL.Query().Get("host") == attackerHost {
			_, _ = w.Write([]byte(`{"project_id":"` + attackerProject + `","aud":"project:` + attackerProject + `"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer stub.Close()
	prevServer := muveeServerURL
	muveeServerURL = stub.URL
	defer func() { muveeServerURL = prevServer }()

	q := url.Values{"host": {attackerHost}, "provider": {"faketwitter"}}
	for k, v := range extra {
		q[k] = v
	}
	r := httptest.NewRequest(http.MethodGet, "/_oauth/start?"+q.Encode(), nil)
	r.Host = "site-b.test"
	r.Header.Set("X-Forwarded-Host", "site-b.test")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handleSSOStart(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("attacker's own /_oauth/start: status %d body %q, want 302 to the provider", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || loc.Query().Get("state") == "" {
		t.Fatalf("no provider state in Location %q (err=%v)", w.Header().Get("Location"), err)
	}
	slot := fwdCookieName(stateSlotBase(apexStateCookieBase, loc.Query().Get("state")))
	c := findCookie(w.Result().Cookies(), slot)
	if c == nil || c.Value == "" {
		t.Fatalf("/_oauth/start set no %s cookie", slot)
	}
	return loc.Query().Get("state"), c
}

// legitCallback is the attacker's OWN browser finishing its own sign-in: the
// real __Host- cookie, so it is accepted. It is the positive control proving
// the harvested state and cookie are genuinely valid — the planted replays
// below are refused only because a sibling cannot deliver them.
func legitCallback(state string, apex *http.Cookie) *http.Request {
	r := plantedCallback(state, nil)
	r.AddCookie(&http.Cookie{Name: apex.Name, Value: apex.Value})
	return r
}

// attackerMintedState: the attacker's own, legitimately minted login state plus
// the sibling-plantable cookies that accompany it.
func attackerMintedState(t *testing.T) (string, map[string]string) {
	t.Helper()
	state, apex := attackerStart(t, url.Values{"state": {"attacker-subsite-nonce"}})
	return state, plantable(state, apex)
}

// plantable is every non-__Host- name a sibling could plant the harvested
// apex state under.
func plantable(state string, apex *http.Cookie) map[string]string {
	return map[string]string{
		apexStateCookieBase:                       apex.Value,
		stateSlotBase(apexStateCookieBase, state): apex.Value,
		"fwd_oauth_state":                         state,
	}
}

// attackerDeviceState: a device flow the attacker started from its own CLI on
// its own host, then pointed at the provider through /_oauth/start?mode=device.
// completed reports whether the victim's sign-in completed it.
func attackerDeviceState(t *testing.T) (string, map[string]string, func() bool) {
	t.Helper()
	state, planted, completed, _ := attackerDevice(t)
	return state, planted, completed
}

func attackerDevice(t *testing.T) (string, map[string]string, func() bool, *http.Cookie) {
	t.Helper()
	deviceCode, userCode := "ATTACKERDEVICECODE", "ATKRCODE"
	entry := &deviceFlowEntry{
		UserCode: userCode, Aud: "project:" + attackerProject, Host: attackerHost,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	deviceFlows.Store(deviceCode, entry)
	deviceByUser.Store(userCode, deviceCode)
	t.Cleanup(func() {
		deviceFlows.Delete(deviceCode)
		deviceByUser.Delete(userCode)
	})
	state, apex := attackerStart(t, url.Values{"mode": {"device"}, "user_code": {userCode}})
	return state, plantable(state, apex),
		func() bool { entry.mu.Lock(); defer entry.mu.Unlock(); return entry.Completed }, apex
}

// Positive control for the signed-state variant: the attacker's own browser,
// carrying the real __Host- cookie, does get signed in. Without this the
// planted-replay test could pass for the wrong reason (e.g. a broken harvest).
func TestBUG002_Control_AttackerOwnBrowserSignsInWithRealCookie(t *testing.T) {
	csrfEnv(t)
	state, apex := attackerStart(t, url.Values{"state": {"attacker-subsite-nonce"}})
	w := httptest.NewRecorder()
	handleOAuthCallback(w, legitCallback(state, apex))
	if _, ok := issuedCredential(w); !ok {
		t.Fatalf("control: the attacker's own sign-in with its real cookie was refused (status %d body %q)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "https://"+attackerHost+"/_oauth/session?code=") {
		t.Errorf("control: Location = %q, want a code hand-off to %s", loc, attackerHost)
	}
}

// Positive control for the device variant: with the real cookie the device
// code does complete, so "not completed" above is attributable to the planting.
func TestBUG002_Control_AttackerOwnBrowserCompletesDeviceWithRealCookie(t *testing.T) {
	csrfEnv(t)
	state, _, completed, apex := attackerDevice(t)
	w := httptest.NewRecorder()
	handleOAuthCallback(w, legitCallback(state, apex))
	if !completed() {
		t.Fatalf("control: the attacker's own device authorization with its real cookie did not complete (status %d body %q)", w.Code, w.Body.String())
	}
}
