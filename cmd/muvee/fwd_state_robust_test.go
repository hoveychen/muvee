package main

// Reproductions for the "invalid state" 400 after a subsite sign-in
// (production, 2026-11-18): a user parked 43 minutes on the provider's page
// and came back after the 10-minute state had expired; and every page load
// ran the whole sign-in chain twice within 2 s, the second /_oauth/start
// overwriting the single state cookie the first one's callback needed.
//
// Fix: one state cookie per sign-in (slots named after the state), a 60-minute
// state lifetime, and a callback that cannot use its state restarts the
// sign-in on the last target instead of answering 400 "invalid state" —
// never accepting the code (BUG-20261117-002).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hoveychen/muvee/internal/api"
)

// browser is a cookie jar per host driving the sign-in handlers the way a
// real browser follows the redirects.
type browser struct {
	t   *testing.T
	jar map[string]map[string]string // host → name → value
}

func newBrowser(t *testing.T) *browser {
	return &browser{t: t, jar: map[string]map[string]string{}}
}

func (b *browser) do(h http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	b.t.Helper()
	host := r.Host
	for n, v := range b.jar[host] {
		r.AddCookie(&http.Cookie{Name: n, Value: v})
	}
	w := httptest.NewRecorder()
	h(w, r)
	if b.jar[host] == nil {
		b.jar[host] = map[string]string{}
	}
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.jar[host], c.Name)
		} else {
			b.jar[host][c.Name] = c.Value
		}
	}
	return w
}

// slots lists the browser's state slots of base on host.
func (b *browser) slots(host, base string) []string {
	var out []string
	for n := range b.jar[host] {
		if strings.HasPrefix(n, hostCookieName(base)+"_") {
			out = append(out, n)
		}
	}
	return out
}

// age re-seals every state slot (and the return URL) the browser holds as if
// it had been issued d ago.
func (b *browser) age(d time.Duration) {
	for host, cookies := range b.jar {
		for n, v := range cookies {
			for base, purpose := range map[string]string{fwdStateCookieBase: "fwd-state", apexStateCookieBase: "apex-state"} {
				if strings.HasPrefix(n, hostCookieName(base)+"_") {
					var raw json.RawMessage
					if _, ok := openValueAt(purpose, v, loginStateLifetime, &raw); !ok {
						b.t.Fatalf("cannot age %s on %s", n, host)
					}
					b.jar[host][n] = sealAt(purpose, raw, time.Now().Add(-d))
				}
			}
		}
	}
}

// loginStart is a click on the provider button of app.site-b.test's login
// page up to the provider: the subsite slot, then the central slot + hint.
// It returns the subsite nonce and the provider state f1.
func (b *browser) loginStart() (nonce, f1 string) {
	b.t.Helper()
	w := b.do(handleLoginPage, epReq("GET", epApp, "/_oauth/login", url.Values{"provider": {"faketwitter"}}))
	loc := epLoc(b.t, w)
	if loc.Host != epCentral || loc.Path != "/_oauth/start" {
		b.t.Fatalf("login page Location = %s, want the central /_oauth/start", loc)
	}
	nonce = loc.Query().Get("state")
	w = b.do(handleSSOStart, epReq("GET", epCentral, "/_oauth/start", loc.Query()))
	ploc := epLoc(b.t, w)
	f1 = ploc.Query().Get("state")
	if !strings.HasPrefix(f1, fwdStatePrefix) {
		b.t.Fatalf("start Location = %s, want the provider with an f1 state", ploc)
	}
	return nonce, f1
}

// callback is the provider's redirect back to the central host.
func (b *browser) callback(f1, code string) *httptest.ResponseRecorder {
	b.t.Helper()
	r := epReq("GET", epCentral, "/_oauth/faketwitter", url.Values{"code": {code}, "state": {f1}})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "faketwitter")
	return b.do(handleOAuthCallback, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
}

// exchange follows a callback's code hand-off to the subsite.
func (b *browser) exchange(cb *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	b.t.Helper()
	loc := epLoc(b.t, cb)
	if loc.Host != epApp || loc.Path != "/_oauth/session" {
		b.t.Fatalf("callback Location = %s, want %s/_oauth/session", loc, epApp)
	}
	r := epReq("GET", epApp, "/_oauth/session", loc.Query())
	r.Header.Set(routeAudienceHeader, epAud)
	return b.do(handleSSOSession, r)
}

// signedIn reports whether the exchange issued app's own session.
func signedIn(w *httptest.ResponseRecorder) bool {
	c := findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase))
	if c == nil || c.Value == "" || c.MaxAge < 0 {
		return false
	}
	_, err := parseFwdToken(c.Value, epAud, fwdKindSession)
	return err == nil
}

func robustEnv(t *testing.T) *fakeSubjectProvider {
	t.Helper()
	epEnv(t)
	p := &fakeSubjectProvider{}
	installFakeProvider(t, p)
	return p
}

// ─── (a) a long pause on the provider's page ────────────────────────────────

func TestStateRobust_CallbackAfter43MinutesSignsIn(t *testing.T) {
	robustEnv(t)
	b := newBrowser(t)
	b.loginStart()
	for host, base := range map[string]string{epApp: fwdStateCookieBase, epCentral: apexStateCookieBase} {
		// The slots themselves live for the whole state lifetime: 60 minutes.
		if loginStateLifetime != 60*time.Minute {
			t.Fatalf("loginStateLifetime = %v, want 60m", loginStateLifetime)
		}
		if n := len(b.slots(host, base)); n != 1 {
			t.Fatalf("%s holds %d %s slots, want 1", host, n, base)
		}
	}
	b.age(43 * time.Minute) // the production case: back after 43 minutes
	cb := b.callback(b.lastF1(), "authcode")
	if cb.Code != http.StatusFound {
		t.Fatalf("callback after 43 min: status %d body %q", cb.Code, cb.Body.String())
	}
	if !signedIn(b.exchange(cb)) {
		t.Fatal("exchange after 43 min did not sign in")
	}
}

// lastF1 is the f1 of the browser's only central slot, recovered from its
// sealed nonce.
func (b *browser) lastF1() string {
	b.t.Helper()
	slots := b.slots(epCentral, apexStateCookieBase)
	if len(slots) != 1 {
		b.t.Fatalf("want exactly one central slot, have %v", slots)
	}
	var st apexState
	if !openValue("apex-state", b.jar[epCentral][slots[0]], loginStateLifetime, &st) {
		b.t.Fatal("central slot does not open")
	}
	return apexStateF1(st.Nonce)
}

func TestStateRobust_StateCookiesLastOneHour(t *testing.T) {
	robustEnv(t)
	r := epReq("GET", epApp, "/_oauth/login", url.Values{"provider": {"faketwitter"}})
	w := httptest.NewRecorder()
	handleLoginPage(w, r)
	got := slotSetCookies(w, fwdStateCookieBase, false)
	if len(got) != 1 || got[0].MaxAge != 3600 {
		t.Fatalf("subsite slot cookies %+v, want one with MaxAge 3600", got)
	}
	start := epLoc(t, w)
	w = httptest.NewRecorder()
	handleSSOStart(w, epReq("GET", epCentral, "/_oauth/start", start.Query()))
	got = slotSetCookies(w, apexStateCookieBase, false)
	if len(got) != 1 || got[0].MaxAge != 3600 {
		t.Fatalf("central slot cookies %+v, want one with MaxAge 3600", got)
	}
	if h := findCookie(w.Result().Cookies(), hostCookieName(loginHintCookieBase)); h == nil || h.MaxAge != int(loginHintLifetime.Seconds()) ||
		h.Domain != "" || !h.Secure || !h.HttpOnly || h.Path != "/" {
		t.Fatalf("login hint cookie %+v, want a host-only __Host- cookie for %v", h, loginHintLifetime)
	}
}

// ─── (b) two sign-ins started from one browser ──────────────────────────────

func TestStateRobust_TwoConcurrentSignInsBothComplete(t *testing.T) {
	for _, order := range []string{"second first", "first first"} {
		t.Run(order, func(t *testing.T) {
			robustEnv(t)
			b := newBrowser(t)
			// The doubled chain: both run to the provider before either returns.
			_, f1a := b.loginStart()
			_, f1b := b.loginStart()
			if f1a == f1b {
				t.Fatal("two sign-ins share an f1")
			}
			if n := len(b.slots(epCentral, apexStateCookieBase)); n != 2 {
				t.Fatalf("central holds %d slots, want 2", n)
			}
			if n := len(b.slots(epApp, fwdStateCookieBase)); n != 2 {
				t.Fatalf("subsite holds %d slots, want 2", n)
			}
			first, second := f1a, f1b
			if order == "second first" {
				first, second = f1b, f1a
			}
			// Either one completes...
			if !signedIn(b.exchange(b.callback(first, "code-1"))) {
				t.Fatal("first completed sign-in failed")
			}
			// ...and the other's slots on both hosts are still there and usable.
			if n := len(b.slots(epCentral, apexStateCookieBase)); n != 1 {
				t.Fatalf("after one callback central holds %d slots, want the other one", n)
			}
			if n := len(b.slots(epApp, fwdStateCookieBase)); n != 1 {
				t.Fatalf("after one exchange the subsite holds %d slots, want the other one", n)
			}
			if !signedIn(b.exchange(b.callback(second, "code-2"))) {
				t.Fatal("the other sign-in no longer completes")
			}
		})
	}
}

// A failing callback deletes only its own slot.
func TestStateRobust_FailedCallbackKeepsOtherSlots(t *testing.T) {
	robustEnv(t)
	b := newBrowser(t)
	_, f1 := b.loginStart()
	cb := b.callback(apexStateF1(randomToken(18)), "stray") // some other, unknown sign-in
	if _, ok := issuedCredential(cb); ok {
		t.Fatal("stray callback issued a credential")
	}
	if len(slotSetCookies(cb, apexStateCookieBase, true)) != 0 {
		t.Fatalf("stray callback deleted a slot: %+v", cb.Result().Cookies())
	}
	if !signedIn(b.exchange(b.callback(f1, "authcode"))) {
		t.Fatal("the real sign-in did not survive a stray callback")
	}
}

// Subsite side on its own: two states, the exchange of one leaves the other.
func TestStateRobust_SubsiteSlotsIndependent(t *testing.T) {
	robustEnv(t)
	b := newBrowser(t)
	var nonces []string
	for i := 0; i < 2; i++ {
		w := b.do(handleLoginPage, epReq("GET", epApp, "/_oauth/login", url.Values{"provider": {"faketwitter"}}))
		nonces = append(nonces, epLoc(t, w).Query().Get("state"))
	}
	for _, nonce := range []string{nonces[1], nonces[0]} {
		code := mintSSOCode(&ssoCode{Host: epApp, Aud: epAud, StateDigest: stateDigest(nonce), SSOSID: "s", AuthTime: time.Now().Unix(), ID: epIdentity})
		r := epReq("GET", epApp, "/_oauth/session", url.Values{"code": {code}, "state": {nonce}})
		r.Header.Set(routeAudienceHeader, epAud)
		if !signedIn(b.do(handleSSOSession, r)) {
			t.Fatalf("exchange for nonce %q failed", nonce)
		}
	}
}

// Abandoned sign-ins do not pile up: at most maxStateSlots per host, the
// newest still works.
func TestStateRobust_StartPrunesOldSlots(t *testing.T) {
	robustEnv(t)
	b := newBrowser(t)
	var f1 string
	for i := 0; i < maxStateSlots+3; i++ {
		_, f1 = b.loginStart()
	}
	if n := len(b.slots(epCentral, apexStateCookieBase)); n != maxStateSlots {
		t.Fatalf("central slots = %d, want %d", n, maxStateSlots)
	}
	if n := len(b.slots(epApp, fwdStateCookieBase)); n != maxStateSlots {
		t.Fatalf("subsite slots = %d, want %d", n, maxStateSlots)
	}
	b.age(61 * time.Minute) // expired slots are dropped by the next start
	_, f1 = b.loginStart()
	if n := len(b.slots(epCentral, apexStateCookieBase)); n != 1 {
		t.Fatalf("central slots after expiry = %d, want 1", n)
	}
	if !signedIn(b.exchange(b.callback(f1, "authcode"))) {
		t.Fatal("newest sign-in failed")
	}
}

// ─── (c) expired / lost state: restart instead of 400 ───────────────────────

func TestStateRobust_ExpiredStateRestartsSignIn(t *testing.T) {
	p := robustEnv(t)
	b := newBrowser(t)
	_, f1 := b.loginStart()
	b.age(61 * time.Minute) // past the 60-minute state
	cb := b.callback(f1, "authcode")
	if _, ok := issuedCredential(cb); ok {
		t.Fatal("expired state issued a credential")
	}
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "https://"+epApp+"/_oauth/login" {
		t.Fatalf("expired state: status %d Location %q body %q, want 302 to %s/_oauth/login", cb.Code, cb.Header().Get("Location"), cb.Body.String(), epApp)
	}
	if p.gotState != "" {
		t.Fatal("the provider code was exchanged for an unmatched state")
	}
	// The restart is an ordinary sign-in: it completes.
	_, f1 = b.loginStart()
	if !signedIn(b.exchange(b.callback(f1, "authcode"))) {
		t.Fatal("restarted sign-in failed")
	}
}

func TestStateRobust_LostStateRecoveryIsBounded(t *testing.T) {
	robustEnv(t)
	b := newBrowser(t)
	lose := func() *httptest.ResponseRecorder {
		_, f1 := b.loginStart()
		for _, n := range b.slots(epCentral, apexStateCookieBase) {
			delete(b.jar[epCentral], n) // the browser keeps losing the slot
		}
		return b.callback(f1, "authcode")
	}
	for i := 1; i <= maxLoginRoundTrips; i++ {
		if w := lose(); w.Code != http.StatusFound || w.Header().Get("Location") != "https://"+epApp+"/_oauth/login" {
			t.Fatalf("recovery %d: status %d Location %q", i, w.Code, w.Header().Get("Location"))
		}
	}
	w := lose()
	if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
		t.Fatalf("past the bound: status %d Location %q, want the expired page", w.Code, w.Header().Get("Location"))
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "登录已过期") {
		t.Fatalf("past the bound: %q %q, want the friendly HTML page", w.Header().Get("Content-Type"), w.Body.String())
	}
	// A completed sign-in resets the count.
	_, f1 := b.loginStart()
	if !signedIn(b.exchange(b.callback(f1, "authcode"))) {
		t.Fatal("sign-in failed")
	}
	if w := lose(); w.Code != http.StatusFound {
		t.Fatalf("after a completed sign-in: status %d, want a recovery again", w.Code)
	}
	// So does a quiet window.
	b2 := newBrowser(t)
	b = b2
	for i := 0; i <= maxLoginRoundTrips; i++ {
		lose()
	}
	var h loginHint
	if !openValue("login-hint", b.jar[epCentral][hostCookieName(loginHintCookieBase)], loginHintLifetime, &h) {
		t.Fatal("no hint")
	}
	h.R = time.Now().Add(-loginRecoveryWindow - time.Minute).Unix()
	b.jar[epCentral][hostCookieName(loginHintCookieBase)] = sealValue("login-hint", h)
	if w := lose(); w.Code != http.StatusFound {
		t.Fatalf("after a quiet window: status %d, want a recovery again", w.Code)
	}
}

func TestStateRobust_NoHintShowsFriendlyPage(t *testing.T) {
	robustEnv(t)
	cb := newBrowser(t).callback(apexStateF1(randomToken(18)), "authcode")
	if cb.Code != http.StatusBadRequest || cb.Header().Get("Location") != "" {
		t.Fatalf("status %d Location %q", cb.Code, cb.Header().Get("Location"))
	}
	if strings.TrimSpace(cb.Body.String()) == "invalid state" || !strings.Contains(cb.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("plain error instead of the page: %q %q", cb.Header().Get("Content-Type"), cb.Body.String())
	}
}

// The hint is only a suggestion: its host must still be a sign-in target of
// this central host, else the page shows instead of a redirect.
func TestStateRobust_HintTargetRevalidated(t *testing.T) {
	robustEnv(t)
	for _, host := range []string{"evil.com", "app.custom.com", "app.site-a.test", epCentral, "unknown.site-b.test"} {
		b := newBrowser(t)
		b.jar[epCentral] = map[string]string{hostCookieName(loginHintCookieBase): sealValue("login-hint", loginHint{Host: host})}
		cb := b.callback(apexStateF1(randomToken(18)), "authcode")
		if cb.Code != http.StatusBadRequest || cb.Header().Get("Location") != "" {
			t.Errorf("hint %q: status %d Location %q, want the page", host, cb.Code, cb.Header().Get("Location"))
		}
	}
	// A hint a sibling could plant (no __Host- prefix, or forged) is ignored.
	for label, c := range map[string]*http.Cookie{
		"bare name": {Name: loginHintCookieBase, Value: sealValue("login-hint", loginHint{Host: epApp})},
		"forged":    {Name: hostCookieName(loginHintCookieBase), Value: sealAt("other", loginHint{Host: epApp}, time.Now())},
	} {
		b := newBrowser(t)
		b.jar[epCentral] = map[string]string{c.Name: c.Value}
		if cb := b.callback(apexStateF1(randomToken(18)), "x"); cb.Code != http.StatusBadRequest {
			t.Errorf("%s hint: status %d Location %q, want the page", label, cb.Code, cb.Header().Get("Location"))
		}
	}
}

// Subsite /_oauth/session with a lost or expired slot goes back to the login
// page (never a bare 400) — already so, kept.
func TestStateRobust_SubsiteExpiredStateRetries(t *testing.T) {
	robustEnv(t)
	b := newBrowser(t)
	_, f1 := b.loginStart()
	cb := b.callback(f1, "authcode")
	b.age(61 * time.Minute)
	w := b.exchange(cb)
	if signedIn(w) {
		t.Fatal("expired subsite state signed in")
	}
	if loc := epLoc(t, w); loc.Path != "/_oauth/login" || loc.Query().Get("attempt") != "1" {
		t.Fatalf("Location = %s, want /_oauth/login?attempt=1", loc)
	}
}

// ─── (d) BUG-20261117-002: recovery never accepts the code ──────────────────

// The victim has a sign-in of its own in flight (hint + slot); the attacker
// sends it to the callback with the attacker's f1 and code and plants every
// cookie a sibling can. The victim is only sent back to its own target to
// start over; the code is never exchanged; its own sign-in still completes.
func TestStateRobust_BUG002_RecoveryNeverUsesForeignCode(t *testing.T) {
	p := robustEnv(t)
	victim := newBrowser(t)
	_, own := victim.loginStart()

	attacker := newBrowser(t)
	_, attackerF1 := attacker.loginStart()
	for n, v := range attacker.jar[epCentral] {
		// A sibling can plant these only without the __Host- prefix.
		victim.jar[epCentral][strings.TrimPrefix(n, "__Host-")] = v
	}
	cb := victim.callback(attackerF1, "attacker-code")
	if name, ok := issuedCredential(cb); ok {
		t.Fatalf("attacker's code signed the victim in (cookie %s)", name)
	}
	if loc := cb.Header().Get("Location"); strings.Contains(loc, "/_oauth/session") || strings.Contains(loc, "code=") {
		t.Fatalf("recovery handed a code to a subsite: %q", loc)
	}
	if cb.Header().Get("Location") != "https://"+epApp+"/_oauth/login" {
		t.Fatalf("Location = %q, want the victim's own target login entry", cb.Header().Get("Location"))
	}
	if p.gotState != "" {
		t.Fatal("the attacker's code was exchanged with the provider")
	}
	if !signedIn(victim.exchange(victim.callback(own, "victim-code"))) {
		t.Fatal("the victim's own sign-in was disturbed")
	}
}

// ─── (e) the new cookie names never reach a tenant ──────────────────────────

// stripCovers applies the embed-bridge stripCookies semantics: an exact name
// or a "<prefix>*" entry (a bare "*" is ignored).
func stripCovers(list []string, name string) bool {
	for _, e := range list {
		if e == name {
			return true
		}
		if len(e) > 1 && strings.HasSuffix(e, stripPrefixWildcard) && strings.HasPrefix(name, strings.TrimSuffix(e, stripPrefixWildcard)) {
			return true
		}
	}
	return false
}

func pluginDefaultStripList(t *testing.T) []string {
	t.Helper()
	src := mustRead(t, "../../traefik/plugins-local/src/github.com/hoveychen/embed-bridge/embed_bridge.go")
	m := regexp.MustCompile(`(?s)var defaultStripCookies = \[\]string\{(.*?)\n\}`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("defaultStripCookies not found")
	}
	var out []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

func TestStateRobust_EveryCookieSetIsStripped(t *testing.T) {
	robustEnv(t)
	set := map[string]bool{}
	b := newBrowser(t)
	record := func() {
		for _, cookies := range b.jar {
			for n := range cookies {
				set[n] = true
			}
		}
	}
	b.loginStart()
	b.loginStart()
	record()
	_, f1 := b.loginStart()
	if !signedIn(b.exchange(b.callback(f1, "authcode"))) {
		t.Fatal("sign-in failed")
	}
	record()
	b.callback(apexStateF1(randomToken(18)), "x") // recovery path
	record()
	// The dev cookie mode drops the __Host- prefix.
	devInsecureCookies = true
	t.Cleanup(func() { devInsecureCookies = false })
	devB := newBrowser(t)
	b = devB
	b.loginStart()
	record()
	devInsecureCookies = false

	plugin := pluginDefaultStripList(t)
	for _, want := range []string{"__Host-fwd_state_", "__Host-fwd_apex_state_", "__Host-fwd_login_hint", "fwd_state_", "fwd_apex_state_"} {
		found := false
		for n := range set {
			found = found || strings.HasPrefix(n, want)
		}
		if !found {
			t.Errorf("flow never set a cookie starting %q (test not exercising it): %v", want, set)
		}
	}
	for n := range set {
		if !stripCovers(api.FwdAuthCookieNames, n) {
			t.Errorf("cookie %q is not stripped by api.FwdAuthCookieNames", n)
		}
		if !stripCovers(plugin, n) {
			t.Errorf("cookie %q is not stripped by the plugin default", n)
		}
	}
	// Prefix entries stay narrow: tenant cookies that merely look alike pass.
	for _, n := range []string{"fwd_statex", "my_fwd_state_1", "Host-fwd_state_1", "fwd_login_hint_x", "app"} {
		if stripCovers(api.FwdAuthCookieNames, n) {
			t.Errorf("tenant cookie %q would be stripped", n)
		}
	}
}
