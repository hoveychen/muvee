package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// hostCookieName is the on-the-wire name of a host-only auth cookie in the
// production (non-dev) cookie mode: "__Host-" + base.
func hostCookieName(base string) string { return fwdCookieName(base) }

// subsiteSlotName / apexSlotName are the on-the-wire names of one sign-in's
// state cookie: the subsite slot keyed by its nonce, the central slot keyed
// by its provider state f1.
func subsiteSlotName(nonce string) string {
	return hostCookieName(stateSlotBase(fwdStateCookieBase, nonce))
}
func apexSlotName(f1 string) string { return hostCookieName(stateSlotBase(apexStateCookieBase, f1)) }

// slotSetCookies returns the Set-Cookie headers of w that write (deleted=false)
// or delete (deleted=true) a state slot of base.
func slotSetCookies(w *httptest.ResponseRecorder, base string, deleted bool) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, hostCookieName(base)+"_") && (c.MaxAge < 0) == deleted {
			out = append(out, c)
		}
	}
	return out
}

// subsiteReq is a request arriving on a subsite (tenant) host.
func subsiteReq(host string) *http.Request {
	r := httptest.NewRequest("GET", "/_oauth/login", nil)
	r.Host = host
	r.Header.Set("X-Forwarded-Host", host)
	r.Header.Set("X-Forwarded-Proto", "https")
	return r
}

// carryCookies replays what a browser would keep from w onto r: every
// Set-Cookie that is not a deletion.
func carryCookies(w *httptest.ResponseRecorder, r *http.Request) {
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			continue
		}
		r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
}

// sealAt is sealValue with a caller-chosen issue time, for expiry tests. It
// mirrors sealValue's wire format (base64url(json{t,v}) "." base64url(mac)).
func sealAt(purpose string, v any, issued time.Time) string {
	b, _ := json.Marshal(struct {
		T int64 `json:"t"`
		V any   `json:"v"`
	}{issued.Unix(), v})
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sealMAC(purpose, b))
}

func flipByte(s string, i int) string {
	b := []byte(s)
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

// ─── Subsite state (__Host-fwd_state) ───────────────────────────────────────

// -007: 签名的 host 绑定 state（signFwdState/verifyFwdState，基域 cookie 携带 host）→
// 子站自己的 host-only __Host-fwd_state 封存 cookie（writeSubsiteState/readSubsiteState）。
func TestSubsiteState_RoundTrip(t *testing.T) {
	withStateSecret(t)
	host := "datamap.site-b.test"
	w := httptest.NewRecorder()
	nonce := writeSubsiteState(w, subsiteReq(host), 1, true)
	if strings.ContainsAny(nonce, "&=+/ ") || len(nonce) == 0 {
		t.Fatalf("nonce %q is not URL-safe / empty", nonce)
	}
	c := findCookie(w.Result().Cookies(), subsiteSlotName(nonce))
	if c == nil {
		t.Fatalf("no %s cookie written: %v", subsiteSlotName(nonce), w.Result().Cookies())
	}
	if !strings.HasPrefix(c.Name, "__Host-fwd_state_") || len(c.Name) != len("__Host-fwd_state_")+12 || c.Domain != "" || c.Path != "/" || !c.Secure || !c.HttpOnly ||
		c.SameSite != http.SameSiteLaxMode || c.MaxAge <= 0 || c.MaxAge > int(loginStateLifetime.Seconds()) {
		t.Fatalf("subsite state cookie is not a host-only __Host- cookie: %+v", c)
	}
	r := subsiteReq(host)
	carryCookies(w, r)
	st, ok := readSubsiteState(r, nonce)
	if !ok {
		t.Fatal("fresh subsite state rejected")
	}
	if st.Nonce != nonce || st.Host != host || st.Trips != 1 || !st.Popup {
		t.Fatalf("round trip lost fields: %+v (nonce %q)", st, nonce)
	}
	// Each sign-in attempt gets its own unpredictable nonce.
	w2 := httptest.NewRecorder()
	if n2 := writeSubsiteState(w2, subsiteReq(host), 0, false); n2 == nonce {
		t.Fatal("two states for the same host share a nonce")
	}
	// WeCom caps the provider state at 128 bytes; the subsite nonce is also
	// forwarded as a query parameter, so it must stay far below that.
	if len(nonce) > 64 {
		t.Fatalf("nonce is %d bytes", len(nonce))
	}
}

func TestSubsiteState_BoundToHost(t *testing.T) {
	withStateSecret(t)
	w := httptest.NewRecorder()
	nonce := writeSubsiteState(w, subsiteReq("datamap.site-b.test"), 0, false)
	for _, other := range []string{"evil.site-b.test", "datamap.site-a.test", "site-b.test"} {
		r := subsiteReq(other)
		carryCookies(w, r)
		if _, ok := readSubsiteState(r, nonce); ok {
			t.Errorf("state minted on datamap.site-b.test accepted on %s", other)
		}
	}
	// Host comparison is case-insensitive (NormalizeHost), same host still ok.
	r := subsiteReq("DataMap.Site-B.test")
	carryCookies(w, r)
	if _, ok := readSubsiteState(r, nonce); !ok {
		t.Error("same host with different case rejected")
	}
}

func TestSubsiteState_RejectsTamperedExpiredForeign(t *testing.T) {
	withStateSecret(t)
	host := "datamap.site-b.test"
	good := subsiteState{Host: host, Nonce: "n0nce"}
	valid := sealValue("fwd-state", good)
	read := func(cookies ...*http.Cookie) bool {
		r := subsiteReq(host)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		_, ok := readSubsiteState(r, good.Nonce)
		return ok
	}
	name := subsiteSlotName(good.Nonce)
	if !read(&http.Cookie{Name: name, Value: valid}) {
		t.Fatal("control: valid sealed state rejected")
	}

	bad := map[string]string{
		"tampered payload":     flipByte(valid, 3),
		"tampered mac":         flipByte(valid, len(valid)-3),
		"truncated":            valid[:len(valid)-2],
		"trailing garbage":     valid + "x",
		"empty":                "",
		"no separator":         strings.ReplaceAll(valid, ".", ""),
		"not base64":           "!!!.???",
		"old numeric state":    fmt.Sprintf("%d", time.Now().UnixNano()),
		"old device- state":    "device-123",
		"old signed state":     "f1." + strings.Repeat("A", 32),
		"expired":              sealAt("fwd-state", good, time.Now().Add(-loginStateLifetime-time.Minute)),
		"issued in future":     sealAt("fwd-state", good, time.Now().Add(10*time.Minute)),
		"other purpose":        sealValue("apex-state", good),
		"cookie of other host": sealValue("fwd-state", subsiteState{Host: "evil.site-b.test", Nonce: "n"}),
	}
	for label, v := range bad {
		if read(&http.Cookie{Name: name, Value: v}) {
			t.Errorf("%s: readSubsiteState accepted %q", label, v)
		}
	}
	if read() {
		t.Error("state accepted without a cookie")
	}
	// Names a sibling subdomain (or an old release) could plant with
	// Domain=<base>: only the __Host- name is honoured.
	// The pre-slot single __Host-fwd_state and another nonce's slot are not
	// this sign-in's slot either.
	for _, n := range []string{"fwd_state", "fwd_oauth_state", "fwd_oauth_host", "__Host-fwd_apex_state",
		stateSlotBase(fwdStateCookieBase, good.Nonce), "__Host-fwd_state", subsiteSlotName("other-nonce"),
		apexSlotName(good.Nonce)} {
		if read(&http.Cookie{Name: n, Value: valid}) {
			t.Errorf("state accepted from cookie %q", n)
		}
	}
	// A different signing secret (rotated / forged) is refused.
	jwtSecret = []byte("other-secret")
	if read(&http.Cookie{Name: name, Value: valid}) {
		t.Error("state signed with a different secret accepted")
	}
}

// -007: 旧 login-token 结构化 state 可冒充 fwd state（verifyFwdState 拒绝）→ 它同样不能当子站 state / apex state 使用。
func TestSealedStates_RejectLoginTokenState(t *testing.T) {
	withStateSecret(t)
	lt, _ := signState(stateClaims{Mode: "login-token", LoginToken: "x"})
	r := subsiteReq("datamap.site-b.test")
	r.AddCookie(&http.Cookie{Name: subsiteSlotName(lt), Value: lt})
	if _, ok := readSubsiteState(r, lt); ok {
		t.Error("login-token state accepted as subsite state")
	}
	r = subsiteReq("site-b.test")
	r.AddCookie(&http.Cookie{Name: apexSlotName(lt), Value: lt})
	if _, ok := readApexState(httptest.NewRecorder(), r, lt); ok {
		t.Error("login-token state accepted as apex state")
	}
}

// ─── Apex state (f1 + __Host-fwd_apex_state) ────────────────────────────────

func TestApexStateF1(t *testing.T) {
	withStateSecret(t)
	n1, n2 := randomToken(18), randomToken(18)
	f1 := apexStateF1(n1)
	if !strings.HasPrefix(f1, fwdStatePrefix) || strings.ContainsAny(f1, "&=+/ ") {
		t.Fatalf("f1 %q is not a URL-safe f1. state", f1)
	}
	if len(f1) > 128 {
		t.Fatalf("f1 is %d bytes; WeCom caps state at 128", len(f1))
	}
	if apexStateF1(n1) != f1 {
		t.Fatal("f1 is not deterministic for a nonce")
	}
	if apexStateF1(n2) == f1 {
		t.Fatal("different nonces give the same f1")
	}
	if !apexStateF1Matches(f1, n1) {
		t.Fatal("f1 does not match its own nonce")
	}
	for label, c := range map[string]struct{ state, nonce string }{
		"other nonce":      {f1, n2},
		"empty nonce":      {apexStateF1(""), ""},
		"empty state":      {"", n1},
		"no f1 prefix":     {strings.TrimPrefix(f1, fwdStatePrefix), n1},
		"tampered":         {flipByte(f1, len(f1)-2), n1},
		"truncated":        {f1[:len(f1)-2], n1},
		"extended":         {f1 + "A", n1},
		"old numeric":      {fmt.Sprintf("%d", time.Now().UnixNano()), n1},
		"old device- one":  {"device-" + n1, n1},
		"the nonce itself": {n1, n1},
	} {
		if apexStateF1Matches(c.state, c.nonce) {
			t.Errorf("%s: apexStateF1Matches(%q, %q) = true", label, c.state, c.nonce)
		}
	}
	jwtSecret = []byte("other-secret")
	if apexStateF1Matches(f1, n1) {
		t.Error("f1 minted under a different secret accepted")
	}
}

func centralReq(withState string, cookies ...*http.Cookie) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequest("GET", "/_oauth/google?code=x&state="+url.QueryEscape(withState), nil)
	r.Host = "site-b.test"
	r.Header.Set("X-Forwarded-Host", "site-b.test")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return httptest.NewRecorder(), r
}

// apexCookie is the central state cookie a browser holds for the sign-in
// whose provider state is f1.
func apexCookie(f1, v string) *http.Cookie {
	return &http.Cookie{Name: apexSlotName(f1), Value: v}
}

func TestReadApexState(t *testing.T) {
	withStateSecret(t)
	st := apexState{Host: "datamap.site-b.test", Aud: "project:p1", State: "sub-state", Nonce: randomToken(18), Mode: "login", Provider: "google"}
	sealed := sealValue("apex-state", st)
	f1 := apexStateF1(st.Nonce)

	isCleared := func(w *httptest.ResponseRecorder) bool {
		c := findCookie(w.Result().Cookies(), apexSlotName(f1))
		return c != nil && c.MaxAge < 0 && c.Value == ""
	}

	t.Run("valid and single use", func(t *testing.T) {
		w, r := centralReq(f1, apexCookie(f1, sealed))
		got, ok := readApexState(w, r, f1)
		if !ok || got != st {
			t.Fatalf("readApexState = %+v, %v; want %+v", got, ok, st)
		}
		// Single use: the cookie is deleted in the same response, so the
		// browser has nothing to replay with the provider's callback URL.
		if !isCleared(w) {
			t.Fatalf("apex state cookie not cleared after use: %v", w.Result().Cookies())
		}
		w2, r2 := centralReq(f1) // what the browser sends after applying the deletion
		carryCookies(w, r2)
		if _, ok := readApexState(w2, r2, f1); ok {
			t.Fatal("second use of the same callback accepted")
		}
	})
	t.Run("f1 mismatch rejected, other sign-ins' slots untouched", func(t *testing.T) {
		for label, q := range map[string]string{
			"other nonce's f1":             apexStateF1(randomToken(18)),
			"empty":                        "",
			"cookie sealed value as state": sealed,
			"legacy numeric":               fmt.Sprintf("%d", time.Now().UnixNano()),
			"device- legacy":               "device-" + st.Nonce,
		} {
			// The browser holds this sign-in's genuine slot; a callback with
			// another state must neither accept it nor delete it.
			w, r := centralReq(q, apexCookie(f1, sealed))
			if _, ok := readApexState(w, r, q); ok {
				t.Errorf("%s: accepted", label)
			}
			if c := findCookie(w.Result().Cookies(), apexSlotName(f1)); c != nil {
				t.Errorf("%s: another sign-in's slot was touched: %+v", label, c)
			}
		}
		// A value under the callback's own slot that does not match its f1
		// (sealed for another nonce) is refused and burned.
		q := apexStateF1(randomToken(18))
		w, r := centralReq(q, apexCookie(q, sealed))
		if _, ok := readApexState(w, r, q); ok {
			t.Fatal("slot holding another nonce's state accepted")
		}
		if c := findCookie(w.Result().Cookies(), apexSlotName(q)); c == nil || c.MaxAge >= 0 {
			t.Fatalf("mismatching slot not burned: %+v", c)
		}
	})
	t.Run("cookie missing rejected", func(t *testing.T) {
		w, r := centralReq(f1)
		if _, ok := readApexState(w, r, f1); ok {
			t.Fatal("accepted without the apex state cookie")
		}
	})
	t.Run("tampered, expired, foreign-purpose cookies rejected", func(t *testing.T) {
		for label, v := range map[string]string{
			"tampered":      flipByte(sealed, 3),
			"bad mac":       flipByte(sealed, len(sealed)-3),
			"truncated":     sealed[:len(sealed)-2],
			"expired":       sealAt("apex-state", st, time.Now().Add(-loginStateLifetime-time.Minute)),
			"other purpose": sealValue("fwd-state", st),
			"garbage":       "x",
		} {
			w, r := centralReq(f1, apexCookie(f1, v))
			if _, ok := readApexState(w, r, f1); ok {
				t.Errorf("%s: accepted", label)
			}
		}
	})
	// -007: 基域 cookie fwd_oauth_state/fwd_oauth_host + 签名 host-bound state + 升级前数字 state
	// 均可被兄弟子域种下（BUG-20261117-002 登录 CSRF）→ 一律不认。
	t.Run("legacy shared cookies and legacy states rejected", func(t *testing.T) {
		legacy := fmt.Sprintf("%d", time.Now().UnixNano())
		for label, cookies := range map[string][]*http.Cookie{
			"numeric state + shared fwd_oauth_state": {{Name: "fwd_oauth_state", Value: legacy}},
			"shared state + host cookie":             {{Name: "fwd_oauth_state", Value: legacy}, {Name: "fwd_oauth_host", Value: "datamap.site-b.test"}},
			"bare-name apex cookie":                  {{Name: apexStateCookieBase, Value: sealed}},
			"bare-name apex slot (dev name)":         {{Name: stateSlotBase(apexStateCookieBase, f1), Value: sealed}},
			"pre-slot single __Host- apex cookie":    {{Name: hostCookieName(apexStateCookieBase), Value: sealed}},
		} {
			w, r := centralReq(legacy, cookies...)
			if _, ok := readApexState(w, r, legacy); ok {
				t.Errorf("%s: accepted", label)
			}
			w, r = centralReq(f1, cookies...)
			if _, ok := readApexState(w, r, f1); ok {
				t.Errorf("%s (with a real f1): accepted", label)
			}
		}
	})
	jwtSecret = []byte("other-secret")
	t.Run("different secret rejected", func(t *testing.T) {
		w, r := centralReq(f1, apexCookie(f1, sealed))
		if _, ok := readApexState(w, r, f1); ok {
			t.Fatal("cookie sealed with another secret accepted")
		}
	})
}

// ─── Subsite login page → central /_oauth/start ─────────────────────────────

// -007: provider 起点发放签名 state + 基域 cookie fwd_oauth_state/fwd_oauth_host →
// 子站只写 host-only __Host-fwd_state，并带随机 nonce 去中心主机 /_oauth/start；不再写任何基域 cookie。
func TestLoginPage_ProviderStartUsesSubsiteState(t *testing.T) {
	withSingleGoogleProject(t)
	withStateSecret(t)
	r := subsiteReq("datamap.site-b.test")
	r.URL = &url.URL{Path: "/_oauth/login", RawQuery: "provider=google"}
	w := httptest.NewRecorder()
	handleLoginPage(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d", w.Code)
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme+"://"+u.Host != "https://site-b.test" || u.Path != "/_oauth/start" {
		t.Fatalf("Location = %s, want the central host's /_oauth/start", u)
	}
	q := u.Query()
	if q.Get("host") != "datamap.site-b.test" || q.Get("provider") != "google" || q.Get("state") == "" {
		t.Fatalf("start query = %v", q)
	}
	r2 := subsiteReq("datamap.site-b.test")
	carryCookies(w, r2)
	st, ok := readSubsiteState(r2, q.Get("state"))
	if !ok || st.Nonce != q.Get("state") {
		t.Fatalf("__Host-fwd_state does not carry the forwarded state: ok=%v st=%+v state=%q", ok, st, q.Get("state"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Domain != "" {
			t.Errorf("login start wrote a base-domain cookie: %+v", c)
		}
		switch c.Name {
		case "fwd_oauth_state", "fwd_oauth_host", "fwd_oauth_redirect":
			t.Errorf("login start wrote legacy cookie %q", c.Name)
		}
	}
}

// startRequest is GET <central>/_oauth/start for the platform's traefik.<base>
// dashboard host (a sign-in target that needs no server lookup).
func startRequest(provider, state, extra string) *http.Request {
	r := httptest.NewRequest("GET", "/_oauth/start?host=traefik.site-b.test&provider="+provider+"&state="+url.QueryEscape(state)+extra, nil)
	r.Host = "site-b.test"
	r.Header.Set("X-Forwarded-Host", "site-b.test")
	return r
}

func TestSSOStart_WritesApexStateAndSendsF1(t *testing.T) {
	withFakeCallbackUpstream(t)
	w := httptest.NewRecorder()
	handleSSOStart(w, startRequest("faketwitter", "sub-state-1", ""))
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	c := findCookie(w.Result().Cookies(), apexSlotName(loc.Query().Get("state")))
	if c == nil || c.Domain != "" || !c.Secure || !c.HttpOnly || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("apex state cookie %+v is not a host-only __Host- cookie", c)
	}
	var st apexState
	if !openValue("apex-state", c.Value, loginStateLifetime, &st) {
		t.Fatal("apex state cookie does not open")
	}
	if st.Host != "traefik.site-b.test" || st.Aud != "host:traefik.site-b.test" || st.State != "sub-state-1" ||
		st.Mode != "login" || st.Provider != "faketwitter" || st.Nonce == "" {
		t.Fatalf("apex state = %+v", st)
	}
	if got := loc.Query().Get("state"); got != apexStateF1(st.Nonce) {
		t.Fatalf("provider state = %q, want f1 of the cookie nonce %q", got, apexStateF1(st.Nonce))
	}
	// The provider never sees the subsite's state or any host.
	if strings.Contains(loc.String(), "sub-state-1") {
		t.Fatalf("subsite state leaked to the provider: %s", loc)
	}
}

func TestSSOStart_RefusesNonCentralHostAndBadState(t *testing.T) {
	withFakeCallbackUpstream(t)
	// A tenant host is not the central host: /_oauth/start is not served there.
	r := startRequest("faketwitter", "s", "")
	r.Host, r.Header["X-Forwarded-Host"] = "datamap.site-b.test", []string{"datamap.site-b.test"}
	w := httptest.NewRecorder()
	handleSSOStart(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("subsite host: status=%d, want 404", w.Code)
	}
	// Missing / oversize subsite state is refused and no cookie is set.
	for _, s := range []string{"", strings.Repeat("a", 129)} {
		w = httptest.NewRecorder()
		handleSSOStart(w, startRequest("faketwitter", s, ""))
		if w.Code != http.StatusBadRequest || len(w.Result().Cookies()) != 0 {
			t.Fatalf("state %q: status=%d cookies=%v, want 400 and none", s, w.Code, w.Result().Cookies())
		}
	}
}

// ─── Provider callback (central host) ───────────────────────────────────────

// callbackRequest is the provider's redirect back to the central host.
func callbackRequest(state string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/_oauth/faketwitter?code=authcode&state="+url.QueryEscape(state), nil)
	r.Host = "site-b.test"
	r.Header.Set("X-Forwarded-Host", "site-b.test")
	for _, c := range cookies {
		r.AddCookie(c)
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

func loginApexState(host, aud, subsiteState string) apexState {
	return apexState{Host: host, Aud: aud, State: subsiteState, Nonce: randomToken(18), Mode: "login", Provider: "faketwitter"}
}

// -007: 回调直接签子站会话并按 fwd_oauth_redirect 回跳 → 回调只签中心 SSO（host-only __Host-muvee_sso），
// 带一次性 code 302 回发起登录的子站 /_oauth/session；兄弟子站甩来的 fwd_oauth_redirect / 旧 host cookie 无法改向。
func TestOAuthCallback_SignsCentralSSOAndRedirectsToStartingHost(t *testing.T) {
	withFakeCallbackUpstream(t)
	st := loginApexState("datamap.site-b.test", "project:p1", "sub-state-2")
	f1 := apexStateF1(st.Nonce)
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(f1,
		apexCookie(f1, sealValue("apex-state", st)),
		&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://evil.site-b.test/phish"},
		&http.Cookie{Name: hostCookieName(fwdRedirectCookieBase), Value: "https://evil.site-b.test/phish"},
		&http.Cookie{Name: "fwd_oauth_host", Value: "evil.site-b.test"},
		&http.Cookie{Name: signedOutHintCookie, Value: "1"},
	))
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://datamap.site-b.test/_oauth/session" {
		t.Fatalf("Location = %s, want the starting subsite's /_oauth/session", loc)
	}
	if loc.Query().Get("state") != "sub-state-2" || loc.Query().Get("code") == "" {
		t.Fatalf("Location query = %v", loc.Query())
	}
	code, ok := takeSSOCode(loc.Query().Get("code"))
	if !ok || code.Host != "datamap.site-b.test" || code.Aud != "project:p1" ||
		code.StateDigest != stateDigest("sub-state-2") || code.ID.UserID != "u1" {
		t.Fatalf("minted code = %+v, ok=%v", code, ok)
	}
	sso := findCookie(w.Result().Cookies(), hostCookieName(ssoCookieBase))
	if sso == nil || sso.Value == "" || sso.Domain != "" || !sso.Secure || !sso.HttpOnly || sso.Path != "/" {
		t.Fatalf("SSO cookie %+v is not a host-only __Host- cookie", sso)
	}
	if _, err := parseFwdToken(sso.Value, ssoAudience("site-b.test"), fwdKindSSO); err != nil {
		t.Fatalf("SSO cookie is not a valid SSO token for the central host: %v", err)
	}
	if _, err := parseFwdToken(sso.Value, "project:p1", fwdKindSession, fwdKindSSO); err == nil {
		t.Fatal("SSO token accepted as a project session")
	}
	if c := findCookie(w.Result().Cookies(), apexSlotName(f1)); c == nil || c.MaxAge >= 0 {
		t.Fatalf("apex state cookie not burned: %+v", c)
	}
	// The central callback never issues a subsite session itself.
	if c := findCookie(w.Result().Cookies(), hostCookieName(fwdSessionCookieBase)); c != nil && c.MaxAge >= 0 {
		t.Fatalf("central callback issued a subsite session cookie: %+v", c)
	}
}

// -007: state 存在即可（含旧数字 state 15 分钟窗口、device- 前缀 state、签名 host-bound state、共享 cookie 对比）→
// 只认 apex 封存 cookie + f1；其余全部 400，且不会签发任何 cookie / code。
func TestOAuthCallback_OldStateFormatsAndSharedCookiesRejected(t *testing.T) {
	withFakeCallbackUpstream(t)
	st := loginApexState("datamap.site-b.test", "project:p1", "sub")
	sealed := sealValue("apex-state", st)
	f1 := apexStateF1(st.Nonce)
	legacy := fmt.Sprintf("%d", time.Now().UnixNano())
	cases := map[string]*http.Request{
		"old numeric state matching shared cookie": callbackRequest(legacy, &http.Cookie{Name: "fwd_oauth_state", Value: legacy}),
		"old numeric state, no cookie":             callbackRequest(legacy),
		"old device- state matching shared cookie": callbackRequest("device-"+newStateNonce(), &http.Cookie{Name: "fwd_oauth_state", Value: "device-x"}),
		"old signed state + host cookie": callbackRequest("fwd1.abc", &http.Cookie{Name: "fwd_oauth_state", Value: "fwd1.abc"},
			&http.Cookie{Name: "fwd_oauth_host", Value: "datamap.site-b.test"}),
		"valid f1 but no apex cookie":          callbackRequest(f1),
		"valid f1 with only legacy cookies":    callbackRequest(f1, &http.Cookie{Name: "fwd_oauth_state", Value: f1}, &http.Cookie{Name: "fwd_oauth_host", Value: "datamap.site-b.test"}),
		"apex cookie but f1 of another nonce":  callbackRequest(apexStateF1(randomToken(18)), apexCookie(f1, sealed)),
		"apex cookie but cookie sealed as raw": callbackRequest(f1, apexCookie(f1, sealed[:len(sealed)-1]+"A")),
		"apex cookie but empty state":          callbackRequest("", apexCookie(f1, sealed)),
		"apex cookie of another provider": func() *http.Request {
			o := st
			o.Provider = "google"
			return callbackRequest(apexStateF1(o.Nonce), apexCookie(apexStateF1(o.Nonce), sealValue("apex-state", o)))
		}(),
	}
	for label, r := range cases {
		w := httptest.NewRecorder()
		handleOAuthCallback(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d Location=%q, want 400", label, w.Code, w.Header().Get("Location"))
		}
		if c := findCookie(w.Result().Cookies(), hostCookieName(ssoCookieBase)); c != nil && c.MaxAge >= 0 {
			t.Errorf("%s: an SSO cookie was issued", label)
		}
	}
}

// After a callback consumed the apex state cookie the browser no longer has
// it; re-sending the same provider URL (back button, replay) is refused.
func TestOAuthCallback_ReplayAfterCookieBurnedRejected(t *testing.T) {
	withFakeCallbackUpstream(t)
	st := loginApexState("datamap.site-b.test", "project:p1", "sub-3")
	f1 := apexStateF1(st.Nonce)
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(f1, apexCookie(f1, sealValue("apex-state", st))))
	if w.Code != http.StatusFound {
		t.Fatalf("first use: status=%d", w.Code)
	}
	r := callbackRequest(f1)
	carryCookies(w, r) // browser jar after the deletion
	w2 := httptest.NewRecorder()
	handleOAuthCallback(w2, r)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("replay: status=%d, want 400", w2.Code)
	}
}

// End to end on the central host: start writes the apex cookie, the provider
// returns the f1 it was given, the callback accepts exactly that pair.
func TestSSOStartThenCallback_EndToEnd(t *testing.T) {
	withFakeCallbackUpstream(t)
	w := httptest.NewRecorder()
	handleSSOStart(w, startRequest("faketwitter", "sub-e2e", ""))
	if w.Code != http.StatusFound {
		t.Fatalf("start: status=%d body=%s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	f1 := loc.Query().Get("state")
	r := callbackRequest(f1)
	carryCookies(w, r)
	cw := httptest.NewRecorder()
	handleOAuthCallback(cw, r)
	if cw.Code != http.StatusFound {
		t.Fatalf("callback: status=%d body=%s", cw.Code, cw.Body.String())
	}
	back, _ := url.Parse(cw.Header().Get("Location"))
	if back.Host != "traefik.site-b.test" || back.Path != "/_oauth/session" || back.Query().Get("state") != "sub-e2e" {
		t.Fatalf("callback Location = %s", back)
	}
	code, ok := takeSSOCode(back.Query().Get("code"))
	if !ok || code.Aud != "host:traefik.site-b.test" {
		t.Fatalf("code = %+v ok=%v", code, ok)
	}
	// A browser that only holds another sign-in's slot cannot finish this one
	// with this f1: nothing is signed and the code is never used — the
	// browser is only sent back to start over on the last target.
	w2 := httptest.NewRecorder()
	handleSSOStart(w2, startRequest("faketwitter", "sub-other", ""))
	r2 := callbackRequest(f1) // f1 of the first attempt
	carryCookies(w2, r2)      // cookies of the second attempt only
	cw2 := httptest.NewRecorder()
	handleOAuthCallback(cw2, r2)
	if _, ok := issuedCredential(cw2); ok {
		t.Fatal("crossed attempts: a credential was issued")
	}
	if loc := cw2.Header().Get("Location"); cw2.Code != http.StatusFound || loc != "https://traefik.site-b.test/_oauth/login" {
		t.Fatalf("crossed attempts: status=%d Location=%q, want a restart on the target's login entry", cw2.Code, loc)
	}
}

// ─── Device flow through the central host ───────────────────────────────────

func registerDeviceEntry(t *testing.T, userCode, aud, host string) *deviceFlowEntry {
	t.Helper()
	dc := "DC" + randomToken(12)
	e := &deviceFlowEntry{UserCode: userCode, Aud: aud, Host: host, ExpiresAt: time.Now().Add(time.Minute)}
	deviceFlows.Store(dc, e)
	deviceByUser.Store(userCode, dc)
	t.Cleanup(func() { deviceFlows.Delete(dc); deviceByUser.Delete(userCode) })
	return e
}

// -007: device- 前缀 state + devicePending map（TestOAuthCallback_DeviceFlowState）→ 设备流同样走 apexState
// （Mode=device + UserCode），伪造的 device- state / 不匹配的 aud / 不存在的 user code 一律 400。
func TestOAuthCallback_DeviceFlowViaApexState(t *testing.T) {
	withFakeCallbackUpstream(t)
	entry := registerDeviceEntry(t, "DEVCODE1", "project:p1", "datamap.site-b.test")
	st := apexState{Host: "datamap.site-b.test", Aud: "project:p1", Nonce: randomToken(18), Mode: "device", UserCode: "DEVCODE1", Provider: "faketwitter"}
	w := httptest.NewRecorder()
	handleOAuthCallback(w, callbackRequest(apexStateF1(st.Nonce), apexCookie(apexStateF1(st.Nonce), sealValue("apex-state", st))))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Device authorized") {
		t.Fatalf("live device state: status=%d body=%s", w.Code, w.Body.String())
	}
	if id, done := entry.snapshot(); !done || id.UserID != "u1" || id.Provider != "faketwitter" {
		t.Fatalf("device entry after authorize: %+v done=%v", id, done)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == hostCookieName(ssoCookieBase) && c.MaxAge >= 0 {
			t.Fatal("device authorization signed a central SSO session")
		}
	}

	// Rejections: nothing completes.
	other := registerDeviceEntry(t, "DEVCODE2", "project:p2", "other.site-b.test")
	bad := map[string]*http.Request{
		"forged device- state": callbackRequest("device-forged", &http.Cookie{Name: "fwd_oauth_state", Value: "device-forged"}),
		"live user code but old device- state": func() *http.Request {
			live := "device-" + newStateNonce()
			return callbackRequest(live, &http.Cookie{Name: "fwd_oauth_state", Value: live})
		}(),
		"entry bound to another aud": func() *http.Request {
			x := st
			x.UserCode = "DEVCODE2" // entry.Aud = project:p2, state.Aud = project:p1
			return callbackRequest(apexStateF1(x.Nonce), apexCookie(apexStateF1(x.Nonce), sealValue("apex-state", x)))
		}(),
		"unknown user code": func() *http.Request {
			x := st
			x.UserCode = "NOSUCHCODE"
			return callbackRequest(apexStateF1(x.Nonce), apexCookie(apexStateF1(x.Nonce), sealValue("apex-state", x)))
		}(),
		"device state with other nonce": callbackRequest(apexStateF1("x"), apexCookie(apexStateF1("x"), sealValue("apex-state", st))),
	}
	for label, r := range bad {
		w := httptest.NewRecorder()
		handleOAuthCallback(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", label, w.Code)
		}
	}
	if _, done := other.snapshot(); done {
		t.Error("a rejected callback completed another subsite's device entry")
	}
}

func TestSSOStart_DeviceModeBoundToEntry(t *testing.T) {
	withFakeCallbackUpstream(t)
	registerDeviceEntry(t, "DEVSTART", "host:traefik.site-b.test", "traefik.site-b.test")
	registerDeviceEntry(t, "DEVELSE1", "project:p9", "other.site-b.test")

	w := httptest.NewRecorder()
	handleSSOStart(w, startRequest("faketwitter", "", "&mode=device&user_code=devs-tart"))
	if w.Code != http.StatusFound {
		t.Fatalf("device start: status=%d body=%s", w.Code, w.Body.String())
	}
	dloc, _ := url.Parse(w.Header().Get("Location"))
	c := findCookie(w.Result().Cookies(), apexSlotName(dloc.Query().Get("state")))
	var st apexState
	if c == nil || !openValue("apex-state", c.Value, loginStateLifetime, &st) || st.Mode != "device" || st.UserCode != "DEVSTART" {
		t.Fatalf("apex state for device start = %+v (cookie %+v)", st, c)
	}
	for _, uc := range []string{"DEVELSE1", "NOSUCH"} { // entry of another host / unknown
		w = httptest.NewRecorder()
		handleSSOStart(w, startRequest("faketwitter", "", "&mode=device&user_code="+uc))
		if w.Code != http.StatusBadRequest || len(w.Result().Cookies()) != 0 {
			t.Fatalf("user_code %s: status=%d cookies=%v, want 400 without cookies", uc, w.Code, w.Result().Cookies())
		}
	}
}

// ─── Return URL ─────────────────────────────────────────────────────────────

// -007: boundPostLoginRedirect(r, boundHost)——回调落在基域、cookie 可被兄弟子站甩入，绑定发起 host 并
// 把不合规目标改写成「发起项目根」→ 回跳 cookie 已是子站 host-only，只做 sameHostReturnURL：
// 同 host 的路径/绝对地址保留，其余（兄弟子站、别名基域、外站）一律退回相对 "/"（不再改写成绝对项目根）。
func TestSameHostReturnURL(t *testing.T) {
	withRedirectDomains(t)
	tests := []struct {
		name, raw, inbound, want string
	}{
		{"same host absolute kept", "https://datamap.site-b.test/deep?x=1", "datamap.site-b.test", "https://datamap.site-b.test/deep?x=1"},
		{"relative kept", "/after", "datamap.site-b.test", "/after"},
		{"relative with query and invite token kept", "/p?invite_token=T#f", "datamap.site-b.test", "/p?invite_token=T#f"},
		{"sibling refused", "https://evil.site-b.test/phish", "datamap.site-b.test", "/"},
		{"alias base refused", "https://x.site-a.test/", "datamap.site-b.test", "/"},
		{"platform apex refused", "https://site-b.test/", "datamap.site-b.test", "/"},
		{"external refused", "https://evil.com/", "datamap.site-b.test", "/"},
		{"protocol-relative refused", "//evil.com", "datamap.site-b.test", "/"},
		{"empty", "", "datamap.site-b.test", "/"},
		{"non-platform inbound, own absolute", "https://tunnel.cfdemo.com/x", "tunnel.cfdemo.com", "https://tunnel.cfdemo.com/x"},
		{"non-platform inbound, platform absolute refused", "https://datamap.site-b.test/", "evil.com", "/"},
		{"non-platform inbound keeps relative", "/x", "evil.com", "/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameHostReturnURL(tt.raw, tt.inbound); got != tt.want {
				t.Fatalf("sameHostReturnURL(%q, %q) = %q, want %q", tt.raw, tt.inbound, got, tt.want)
			}
		})
	}
}

func formLoginRequest(path, body string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Host = "datamap.site-b.test"
	r.Header.Set("X-Forwarded-Host", "datamap.site-b.test")
	r.AddCookie(&http.Cookie{Name: hostCookieName(fwdRedirectCookieBase), Value: "https://evil.site-b.test/phish"})
	// A base-domain cookie of the old name (tossed by a sibling) is not read at all.
	r.AddCookie(&http.Cookie{Name: "fwd_oauth_redirect", Value: "https://evil.site-b.test/old"})
	return r
}

// -007: 密码/短信登录回跳「发起项目根（绝对）」、会话 cookie muvee_fwd_session 带 Domain=基域 →
// 回跳退回本站相对 "/"；会话 cookie 为 host-only __Host-muvee_fwd，JWT aud=project:<id>，7 天固定，别的项目无效。
func TestFormLogins_RedirectBoundToCurrentHostAndSessionIsolated(t *testing.T) {
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
		sess := findCookie(w.Result().Cookies(), hostCookieName(fwdSessionCookieBase))
		if sess == nil || sess.Value == "" {
			t.Fatalf("%s: no session issued (status %d)", name, w.Code)
		}
		if sess.Name != "__Host-muvee_fwd" || sess.Domain != "" || sess.Path != "/" || !sess.Secure || !sess.HttpOnly ||
			sess.MaxAge != int(projectSessionLifetime.Seconds()) {
			t.Fatalf("%s: session cookie %+v is not a host-only 7-day __Host- cookie", name, sess)
		}
		if old := findCookie(w.Result().Cookies(), "muvee_fwd_session"); old != nil && old.MaxAge >= 0 {
			t.Fatalf("%s: legacy muvee_fwd_session was written: %+v", name, old)
		}
		if c, err := parseFwdToken(sess.Value, "project:p1", fwdKindSession); err != nil || c.ProjectID != "p1" {
			t.Fatalf("%s: session invalid for its own project: claims=%+v err=%v", name, c, err)
		}
		for _, aud := range []string{"project:p2", "host:datamap.site-b.test"} {
			if _, err := parseFwdToken(sess.Value, aud, fwdKindSession); err == nil {
				t.Fatalf("%s: project session accepted for aud %q", name, aud)
			}
		}
		if loc := w.Header().Get("Location"); loc != "/" {
			t.Fatalf("%s: Location=%q, want \"/\" (tossed return URL must not be followed)", name, loc)
		}
	}
}

// ─── Source guards ──────────────────────────────────────────────────────────

// Provider states are no longer a guessable timestamp / device- prefix.
func TestDeviceOAuthStateUnpredictable(t *testing.T) {
	src := mustRead(t, "authservice.go")
	if strings.Contains(src, `fmt.Sprintf("device-%d", time.Now().UnixNano())`) {
		t.Fatal("device flow state is still a timestamp")
	}
	if strings.Contains(src, `state := fmt.Sprintf("%d", time.Now().UnixNano())`) {
		t.Fatal("login state is still a timestamp")
	}
	// -007 (新增): 旧 state 机制的符号与 cookie 名不得回流进实现。
	all := src + mustRead(t, "fwd_sso.go") + mustRead(t, "oauth_state.go")
	for _, banned := range []string{"devicePending", "signFwdState(", "verifyFwdState(", "isFreshLegacyState("} {
		if strings.Contains(all, banned) {
			t.Errorf("legacy state mechanism %q is back in the implementation", banned)
		}
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
