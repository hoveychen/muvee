package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// revokeStub is a muvee-server stand-in for the console-logout revocation
// tests (BACKLOG-20261117-008). It answers both the per-project access check
// and the standalone session check, revoking sessions with iat <= revokedIAT
// when revoked is set, and records every call.
type revokeStub struct {
	mu         sync.Mutex
	calls      []string // path?query
	revoked    bool
	revokedIAT int64
	fail       bool
}

func (s *revokeStub) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r.URL.Path+"?"+r.URL.RawQuery)
}

func (s *revokeStub) callsTo(path string) []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []url.Values
	for _, c := range s.calls {
		p, q, _ := strings.Cut(c, "?")
		if p == path {
			v, _ := url.ParseQuery(q)
			out = append(out, v)
		}
	}
	return out
}

func (s *revokeStub) isRevoked(q url.Values) bool {
	raw := q.Get("session_iat")
	if raw == "" || !s.revoked {
		return false
	}
	var iat int64
	for _, ch := range raw {
		iat = iat*10 + int64(ch-'0')
	}
	return iat <= s.revokedIAT
}

const (
	pathAccessCheck    = "/api/internal/access/check"
	pathSessionRevoked = "/api/internal/auth/session-revoked"
	pathInvite         = "/api/internal/auth/project-invite"
)

func revokeEnv(t *testing.T) *revokeStub {
	t.Helper()
	stub := &revokeStub{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		if stub.fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathAccessCheck:
			if stub.isRevoked(r.URL.Query()) {
				_, _ = w.Write([]byte(`{"allowed":true,"mode":"public","session_revoked":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"allowed":true,"mode":"public"}`))
		case pathSessionRevoked:
			if stub.isRevoked(r.URL.Query()) {
				_, _ = w.Write([]byte(`{"revoked":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"revoked":false}`))
		case "/api/internal/projects/11111111-1111-1111-1111-111111111111":
			_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","name":"P"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
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
	return stub
}

const revokeProjectID = "11111111-1111-1111-1111-111111111111"

// sessionIssuedAtTime signs a roaming session whose iat is at.
func sessionIssuedAtTime(t *testing.T, at time.Time) string {
	t.Helper()
	tok, err := signForwardJWT(testUserID, "a@corp.example", "A", "", "google")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := parseForwardJWT(tok)
	c.IssuedAt = jwt.NewNumericDate(at)
	return mustSign(t, c)
}

// assertSessionCleared checks the response deleted muvee_fwd_session and set
// the signed-out hint (so a single-provider login page shows the selection
// page instead of riding the provider's SSO straight back in).
func assertSessionCleared(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	c := sessionCookieFrom(w)
	if c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("muvee_fwd_session not cleared: %+v", c)
	}
	if c != nil && c.Domain != "example.com" {
		t.Errorf("cleared cookie domain = %q, want example.com", c.Domain)
	}
	if h := findCookie(w.Result().Cookies(), signedOutHintCookie); h == nil || h.MaxAge <= 0 {
		t.Errorf("signed-out hint not set: %+v", h)
	}
}

func TestVerify_RevokedSessionRejected(t *testing.T) {
	stub := revokeEnv(t)
	logout := time.Now().Add(-time.Hour)
	stub.revoked, stub.revokedIAT = true, logout.Unix()

	r := verifyRequest(sessionIssuedAtTime(t, logout.Add(-time.Minute)), revokeProjectID, "")
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("X-Forwarded-Uri", "/dash")
	w := httptest.NewRecorder()
	handleVerify(w, r)

	if w.Code != http.StatusFound || !strings.HasSuffix(w.Header().Get("Location"), "/_oauth/login") {
		t.Fatalf("revoked session: status %d location %q, want 302 to /_oauth/login", w.Code, w.Header().Get("Location"))
	}
	assertSessionCleared(t, w)
	if w.Header().Get("X-Forwarded-User") != "" {
		t.Errorf("revoked session leaked identity headers")
	}
	// Answered by the access check itself: one round trip, no extra call.
	checks := stub.callsTo(pathAccessCheck)
	if len(checks) != 1 || checks[0].Get("session_iat") == "" {
		t.Errorf("access check calls = %v, want one carrying session_iat", checks)
	}
	if n := len(stub.callsTo(pathSessionRevoked)); n != 0 {
		t.Errorf("project verify made %d extra session-check calls, want 0", n)
	}
}

func TestVerify_SessionAfterLogoutAccepted(t *testing.T) {
	stub := revokeEnv(t)
	logout := time.Now().Add(-time.Hour)
	stub.revoked, stub.revokedIAT = true, logout.Unix()

	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(sessionIssuedAtTime(t, logout.Add(time.Minute)), revokeProjectID, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("session issued after the logout: status %d, want 200", w.Code)
	}
}

// A session signed before sessions carried iat counts as issued at 0: any
// revocation on record voids it; with none on record it keeps working.
func TestVerify_LegacySessionWithoutIAT(t *testing.T) {
	stub := revokeEnv(t)
	legacy := mustSign(t, &authClaims{
		Email: "a@corp.example", UserID: testUserID, Provider: "google",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(6 * 24 * time.Hour))},
	})

	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(legacy, revokeProjectID, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("legacy session, no revocation: status %d, want 200", w.Code)
	}
	if got := stub.callsTo(pathAccessCheck)[0].Get("session_iat"); got != "0" {
		t.Errorf("legacy session_iat = %q, want 0", got)
	}

	stub.revoked, stub.revokedIAT = true, time.Now().Add(-24*time.Hour).Unix()
	w = httptest.NewRecorder()
	handleVerify(w, verifyRequest(legacy, revokeProjectID, ""))
	if w.Code == http.StatusOK {
		t.Fatalf("legacy session after a console logout must be rejected")
	}
	assertSessionCleared(t, w)
}

// Sliding renewal must not run for a revoked session, and must keep iat so a
// renewal never carries a session past a later logout.
func TestVerify_RenewalKeepsIATAndSkipsRevoked(t *testing.T) {
	stub := revokeEnv(t)
	issued := time.Now().Add(-3 * 24 * time.Hour)
	tok, _ := signForwardJWTWithExpiry(testUserID, "a@corp.example", "A", "", "google", 4*24*time.Hour)
	c, _ := parseForwardJWT(tok)
	c.IssuedAt = jwt.NewNumericDate(issued)
	aging := mustSign(t, c)

	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(aging, revokeProjectID, ""))
	renewed := sessionCookieFrom(w)
	if w.Code != http.StatusOK || renewed == nil || renewed.Value == "" {
		t.Fatalf("aging session not renewed: status %d cookie %+v", w.Code, renewed)
	}
	rc, err := parseForwardJWT(renewed.Value)
	if err != nil || rc.IssuedAt == nil || rc.IssuedAt.Unix() != issued.Unix() || rc.Kind != fwdKindSession {
		t.Fatalf("renewal changed iat/kind: %+v err=%v", rc, err)
	}

	stub.revoked, stub.revokedIAT = true, time.Now().Add(-time.Hour).Unix()
	w = httptest.NewRecorder()
	handleVerify(w, verifyRequest(renewed.Value, revokeProjectID, ""))
	if w.Code == http.StatusOK {
		t.Fatalf("renewed session issued before the logout must be rejected")
	}
	if c := sessionCookieFrom(w); c == nil || c.Value != "" {
		t.Errorf("revoked session got renewed instead of cleared: %+v", c)
	}
}

// Verify without a project (unbound tunnel) has no access check to ride, so it
// asks the session endpoint.
func TestVerify_NoProjectUsesSessionCheck(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	r := httptest.NewRequest(http.MethodGet, "/verify", nil)
	r.Header.Set("X-Forwarded-Host", "tun.example.com")
	r.AddCookie(&http.Cookie{Name: "muvee_fwd_session", Value: sessionIssuedAtTime(t, time.Now().Add(-time.Hour))})
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("revoked session passed project-less verify")
	}
	assertSessionCleared(t, w)
	if n := len(stub.callsTo(pathSessionRevoked)); n != 1 {
		t.Errorf("session-check calls = %d, want 1", n)
	}
}

// A revoked session must not consume an invite on the user's behalf.
func TestVerify_RevokedSessionDoesNotConsumeInvite(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	r := verifyRequest(sessionIssuedAtTime(t, time.Now().Add(-time.Hour)), revokeProjectID, "")
	r.Header.Set("X-Forwarded-Uri", "/?invite_token=tok123")
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("revoked session passed verify with invite")
	}
	for _, c := range stub.calls {
		if strings.Contains(c, "invite") || strings.Contains(c, "upsert") {
			t.Errorf("revoked session reached invite consumption: %s", c)
		}
	}
}

// The muvee-server being down fails closed, as the access check already does.
func TestVerify_RevocationCheckFailureFailsClosed(t *testing.T) {
	stub := revokeEnv(t)
	stub.fail = true
	r := httptest.NewRequest(http.MethodGet, "/verify", nil)
	r.AddCookie(&http.Cookie{Name: "muvee_fwd_session", Value: sessionIssuedAtTime(t, time.Now())})
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

// kind=cli device-flow tokens are out of scope (BACKLOG-20261117-007) and are
// not checked. An unmarked Bearer token is: sessions and CLI tokens signed
// before the kind claim are indistinguishable, so treating it as CLI would let
// a pre-upgrade session cookie replayed as Bearer dodge console logout. A
// session-kind JWT replayed as Bearer is checked too.
func TestVerify_BearerRevocationByKind(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	cli, _ := signForwardCLIJWT(testUserID, "a@corp.example", "A", "", "google", time.Hour)
	legacyCLI := mustSign(t, &authClaims{
		Email: "a@corp.example", UserID: testUserID, Provider: "google",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	})
	session := sessionIssuedAtTime(t, time.Now().Add(-time.Hour))

	for name, tc := range map[string]struct {
		token string
		ok    bool
	}{"cli": {cli, true}, "legacy-unmarked": {legacyCLI, false}, "session-as-bearer": {session, false}} {
		r := httptest.NewRequest(http.MethodGet, "/verify?project_id="+revokeProjectID, nil)
		r.Header.Set("X-Forwarded-Host", "myproj.example.com")
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handleVerify(w, r)
		if (w.Code == http.StatusOK) != tc.ok {
			t.Errorf("%s: status %d, want ok=%v", name, w.Code, tc.ok)
		}
	}
}

func TestDeviceTokenIsCLIKind(t *testing.T) {
	revokeEnv(t)
	tok, err := signForwardCLIJWT(testUserID, "a@corp.example", "A", "", "google", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := parseForwardJWT(tok)
	if c.Kind != fwdKindCLI || c.IssuedAt == nil {
		t.Errorf("cli token claims = %+v", c)
	}
	src, err := os.ReadFile("authservice.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "token, err := signForwardCLIJWT(entry.UserID") {
		t.Errorf("handleDeviceToken must sign kind=cli tokens via signForwardCLIJWT")
	}
}

// Every other consumer of resolveAuthClaims rejects a revoked session.
func TestRevokedSession_OtherConsumers(t *testing.T) {
	stub := revokeEnv(t)
	adminEmails = map[string]struct{}{"a@corp.example": {}}
	t.Cleanup(func() { adminEmails = nil })
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()
	revoked := sessionIssuedAtTime(t, time.Now().Add(-time.Hour))

	withSession := func(method, target string) *http.Request {
		var r *http.Request
		if method == http.MethodPost {
			r = httptest.NewRequest(method, target, strings.NewReader("project_id="+revokeProjectID+"&reason=x"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, target, nil)
		}
		r.Host = "myproj.example.com"
		r.Header.Set("X-Forwarded-Host", "myproj.example.com")
		r.AddCookie(&http.Cookie{Name: "muvee_fwd_session", Value: revoked})
		return r
	}

	cases := []struct {
		name  string
		h     http.HandlerFunc
		req   *http.Request
		check func(*httptest.ResponseRecorder) bool
	}{
		{"verify-admin", handleVerifyAdmin, withSession(http.MethodGet, "/verify-admin"),
			func(w *httptest.ResponseRecorder) bool { return w.Code != http.StatusOK }},
		{"userinfo", handleUserInfo, withSession(http.MethodGet, "/_oauth/userinfo"),
			func(w *httptest.ResponseRecorder) bool { return w.Code == http.StatusUnauthorized }},
		{"request-access page", handleRequestAccessPage, withSession(http.MethodGet, "/_oauth/request-access?project="+revokeProjectID),
			func(w *httptest.ResponseRecorder) bool {
				return w.Code == http.StatusFound && w.Header().Get("Location") == "/_oauth/login"
			}},
		{"request-access submit", handleRequestAccessSubmit, withSession(http.MethodPost, "/_oauth/request-access"),
			func(w *httptest.ResponseRecorder) bool {
				return w.Code == http.StatusFound && w.Header().Get("Location") == "/_oauth/login"
			}},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		tc.h(w, tc.req)
		if !tc.check(w) {
			t.Errorf("%s: revoked session not rejected: status %d location %q body %q",
				tc.name, w.Code, w.Header().Get("Location"), w.Body.String())
		}
		assertSessionCleared(t, w)
	}
	for _, c := range stub.calls {
		if strings.Contains(c, "submit-request") {
			t.Errorf("revoked session submitted an access request: %s", c)
		}
	}

	// Not revoked → each passes as before.
	stub.revoked = false
	w := httptest.NewRecorder()
	handleUserInfo(w, withSession(http.MethodGet, "/_oauth/userinfo"))
	if w.Code != http.StatusOK {
		t.Errorf("userinfo with a live session: status %d", w.Code)
	}
	w = httptest.NewRecorder()
	handleVerifyAdmin(w, withSession(http.MethodGet, "/verify-admin"))
	if w.Code != http.StatusOK {
		t.Errorf("verify-admin with a live session: status %d", w.Code)
	}
}

// Drift guard: resolveRequestClaims skips the revocation check, so only
// handleVerify (which runs it itself, piggybacking on the access check) may
// call it; everything else goes through resolveAuthClaims.
func TestResolveRequestClaimsOnlyInVerify(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fn := ""
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(line, "func ") {
				fn = line
			}
			if !strings.Contains(line, "resolveRequestClaims(r)") {
				continue
			}
			if !strings.HasPrefix(fn, "func handleVerify(") && !strings.HasPrefix(fn, "func resolveAuthClaims(") {
				t.Errorf("%s: %q calls resolveRequestClaims directly; use resolveAuthClaims", name, fn)
			}
		}
	}
}
