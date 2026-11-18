package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// revokeStub is a muvee-server stand-in for the console-logout revocation
// tests (BACKLOG-20261117-008, -007). It answers both the per-project access
// check and the standalone session check. A credential is revoked when
//   - revoked is set and the session_iat it sends (the credential's auth_time,
//     falling back to its iat) is <= revokedIAT (console logout), or
//   - any of the comma-separated sids it sends is in revokedSIDs (a sign-out
//     that revoked its sid / sso_sid),
//
// and every call is recorded.
type revokeStub struct {
	mu          sync.Mutex
	calls       []string // path?query
	revoked     bool
	revokedIAT  int64
	revokedSIDs map[string]bool
	fail        bool
	// cliRevoked / cliRevokedIAT: the user's CLI-token revocation time point
	// (users.cli_tokens_revoked_at, BACKLOG-20261117-009), compared with the
	// cli_iat a kind=cli credential sends. Independent of revoked / revokedIAT.
	cliRevoked    bool
	cliRevokedIAT int64
}

func (s *revokeStub) revokeSID(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revokedSIDs == nil {
		s.revokedSIDs = map[string]bool{}
	}
	s.revokedSIDs[sid] = true
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sid := range strings.Split(q.Get("sids"), ",") {
		if sid != "" && s.revokedSIDs[sid] {
			return true
		}
	}
	if raw := q.Get("cli_iat"); raw != "" && s.cliRevoked {
		if iat, err := strconv.ParseInt(raw, 10, 64); err == nil && iat <= s.cliRevokedIAT {
			return true
		}
	}
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

const revokeAud = "project:" + revokeProjectID

var revokeIdentity = fwdIdentity{UserID: testUserID, Email: "a@corp.example", Name: "A", Provider: "google"}

// sessionAuthTimeAt signs a roaming subsite session for the revoke project
// whose auth_time is at (the sign-in time console logout is compared with).
func sessionAuthTimeAt(t *testing.T, at time.Time) string {
	t.Helper()
	tok, err := signSubsiteSession(revokeIdentity, revokeAud, "sso-"+randomToken(6), at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// revokeVerifyRequest is the ForwardAuth request Traefik sends for the revoke
// project, carrying session as the host-only __Host-muvee_fwd cookie.
func revokeVerifyRequest(session string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/verify?project_id="+revokeProjectID, nil)
	r.Header.Set("X-Forwarded-Host", "myproj.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: session})
	return r
}

func revokeSessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	return findCookie(w.Result().Cookies(), fwdCookieName(fwdSessionCookieBase))
}

// assertSessionCleared checks the response deleted the host-only session
// cookie and set the signed-out hint (so a single-provider login page shows
// the selection page instead of riding the provider's SSO straight back in).
// -007: the cleared cookie had Domain=example.com (shared base-domain cookie)
// → the session cookie is __Host-muvee_fwd, host-only (no Domain); the legacy
// base-domain muvee_fwd_session is deleted too, with Domain=example.com.
func assertSessionCleared(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	c := revokeSessionCookie(w)
	if c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("%s not cleared: %+v", fwdCookieName(fwdSessionCookieBase), c)
	}
	if c != nil && (c.Domain != "" || !c.Secure) {
		t.Errorf("cleared cookie must be host-only and Secure (__Host- contract): %+v", c)
	}
	if old := findCookie(w.Result().Cookies(), "muvee_fwd_session"); old == nil || old.MaxAge >= 0 || old.Domain != "example.com" {
		t.Errorf("legacy muvee_fwd_session not deleted on the base domain: %+v", old)
	}
	if h := findCookie(w.Result().Cookies(), fwdCookieName(signedOutHintCookie)); h == nil || h.MaxAge <= 0 {
		t.Errorf("signed-out hint not set: %+v", h)
	}
}

// -008/-007: console logout was compared with the session's iat → it is now
// compared with auth_time (sessionAuthTimeAt sets it; the stub receives it as
// session_iat).
func TestVerify_RevokedSessionRejected(t *testing.T) {
	stub := revokeEnv(t)
	logout := time.Now().Add(-time.Hour)
	stub.revoked, stub.revokedIAT = true, logout.Unix()

	r := revokeVerifyRequest(sessionAuthTimeAt(t, logout.Add(-time.Minute)))
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
	handleVerify(w, revokeVerifyRequest(sessionAuthTimeAt(t, logout.Add(time.Minute))))
	if w.Code != http.StatusOK {
		t.Fatalf("session signed in after the logout: status %d, want 200", w.Code)
	}
}

// -007 (new): the access check carries the credential's sid and sso_sid, and a
// sign-out that revoked either one voids the session — the sid of this very
// session (single-site logout), or the sso_sid it was minted from (central
// sign-out kills every subsite session of that SSO) — even though the user's
// console logout time is untouched.
func TestVerify_SIDRevocation(t *testing.T) {
	for _, which := range []string{"sid", "sso_sid"} {
		t.Run(which, func(t *testing.T) {
			stub := revokeEnv(t)
			tok := sessionAuthTimeAt(t, time.Now())
			c, err := parseFwdToken(tok, revokeAud, fwdKindSession)
			if err != nil || c.SID == "" || c.SSOSID == "" {
				t.Fatalf("session claims = %+v err=%v, want both sid and sso_sid", c, err)
			}

			// Not revoked yet: passes, and the check carried both ids.
			w := httptest.NewRecorder()
			handleVerify(w, revokeVerifyRequest(tok))
			if w.Code != http.StatusOK {
				t.Fatalf("live session: status %d, want 200", w.Code)
			}
			sent := stub.callsTo(pathAccessCheck)
			if len(sent) != 1 || sent[0].Get("sids") != c.SID+","+c.SSOSID {
				t.Fatalf("access check sids = %v, want %q", sent, c.SID+","+c.SSOSID)
			}

			if which == "sid" {
				stub.revokeSID(c.SID)
			} else {
				stub.revokeSID(c.SSOSID)
			}
			w = httptest.NewRecorder()
			handleVerify(w, revokeVerifyRequest(tok))
			if w.Code == http.StatusOK {
				t.Fatalf("session with a revoked %s must be rejected", which)
			}
			assertSessionCleared(t, w)
			if w.Header().Get("X-Forwarded-User") != "" {
				t.Errorf("revoked session leaked identity headers")
			}
		})
	}
}

// A session signed before sessions carried iat / auth_time counts as issued at
// 0: any revocation on record voids it; with none on record it keeps working.
// (-007: it still needs aud and kind=session, which every session since
// BACKLOG-20261117-007 carries; only iat / auth_time / sid are absent here.)
func TestVerify_LegacySessionWithoutIAT(t *testing.T) {
	stub := revokeEnv(t)
	legacy := mustSign(t, &authClaims{
		Email: "a@corp.example", UserID: testUserID, Provider: "google", Kind: fwdKindSession,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{revokeAud},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(6 * time.Hour)),
		},
	})

	w := httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(legacy))
	if w.Code != http.StatusOK {
		t.Fatalf("legacy session, no revocation: status %d, want 200", w.Code)
	}
	if got := stub.callsTo(pathAccessCheck)[0].Get("session_iat"); got != "0" {
		t.Errorf("legacy session_iat = %q, want 0", got)
	}

	stub.revoked, stub.revokedIAT = true, time.Now().Add(-24*time.Hour).Unix()
	w = httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(legacy))
	if w.Code == http.StatusOK {
		t.Fatalf("legacy session after a console logout must be rejected")
	}
	assertSessionCleared(t, w)
}

// -007 (new): a session without auth_time (but with iat) is compared by its
// iat, so a token minted before auth_time existed is still voided by a logout.
func TestVerify_AuthTimeFallsBackToIAT(t *testing.T) {
	stub := revokeEnv(t)
	issued := time.Now().Add(-2 * time.Hour)
	tok := mustSign(t, &authClaims{
		Email: "a@corp.example", UserID: testUserID, Provider: "google", Kind: fwdKindSession,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{revokeAud},
			IssuedAt:  jwt.NewNumericDate(issued),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(6 * time.Hour)),
		},
	})
	w := httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if got := stub.callsTo(pathAccessCheck)[0].Get("session_iat"); got != strconv.FormatInt(issued.Unix(), 10) {
		t.Errorf("session_iat = %q, want the iat %d", got, issued.Unix())
	}
}

// Sliding renewal must not run for a revoked session, and must keep sid,
// sso_sid, iat and auth_time so a renewal never carries a session past a later
// logout. -007: the old test pinned only iat (the logout comparison point);
// the comparison point is now auth_time, so it pins auth_time too, and a
// renewal that would move it is the regression to catch.
func TestVerify_RenewalKeepsAuthTimeAndSkipsRevoked(t *testing.T) {
	stub := revokeEnv(t)
	issued := time.Now().Add(-7 * time.Hour)
	tok, err := signSubsiteSession(revokeIdentity, revokeAud, "sso-keep", issued.Unix())
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseFwdToken(tok, revokeAud, fwdKindSession)
	if err != nil {
		t.Fatal(err)
	}
	c.IssuedAt = jwt.NewNumericDate(issued)
	c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(5 * time.Hour)) // < 6h left: due for renewal
	aging := mustSign(t, c)

	w := httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(aging))
	renewed := revokeSessionCookie(w)
	if w.Code != http.StatusOK || renewed == nil || renewed.Value == "" {
		t.Fatalf("aging session not renewed: status %d cookie %+v", w.Code, renewed)
	}
	rc, err := parseFwdToken(renewed.Value, revokeAud, fwdKindSession)
	if err != nil || rc.IssuedAt == nil || rc.IssuedAt.Unix() != issued.Unix() || rc.Kind != fwdKindSession {
		t.Fatalf("renewal changed iat/kind: %+v err=%v", rc, err)
	}
	if rc.AuthTime != issued.Unix() || rc.SID != c.SID || rc.SSOSID != "sso-keep" {
		t.Errorf("renewal must keep auth_time/sid/sso_sid: got auth_time=%d sid=%q sso_sid=%q, want %d %q sso-keep",
			rc.AuthTime, rc.SID, rc.SSOSID, issued.Unix(), c.SID)
	}
	if !rc.ExpiresAt.After(c.ExpiresAt.Time) {
		t.Errorf("renewal did not extend the expiry: %v vs %v", rc.ExpiresAt, c.ExpiresAt)
	}

	// Console logout after the sign-in: the renewed session (fresh expiry, old
	// auth_time) is voided and not renewed again.
	stub.revoked, stub.revokedIAT = true, time.Now().Add(-time.Hour).Unix()
	w = httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(renewed.Value))
	if w.Code == http.StatusOK {
		t.Fatalf("renewed session signed in before the logout must be rejected")
	}
	if c := revokeSessionCookie(w); c == nil || c.Value != "" {
		t.Errorf("revoked session got renewed instead of cleared: %+v", c)
	}
}

// -007 (new): re-signing a session (renewal) refreshes its iat, but console
// logout compares auth_time: a session whose iat is after the logout yet whose
// auth_time is before it is revoked. (Comparing iat, as before, would let a
// renewal after the logout resurrect the session.)
func TestVerify_LogoutComparesAuthTimeNotIAT(t *testing.T) {
	stub := revokeEnv(t)
	logout := time.Now().Add(-time.Hour)
	stub.revoked, stub.revokedIAT = true, logout.Unix()

	tok, err := signSubsiteSession(revokeIdentity, revokeAud, "sso-x", logout.Add(-time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := parseFwdToken(tok, revokeAud, fwdKindSession)
	if c.IssuedAt.Unix() <= logout.Unix() {
		t.Fatalf("test setup: iat %d must be after the logout %d", c.IssuedAt.Unix(), logout.Unix())
	}
	w := httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(tok))
	if w.Code == http.StatusOK {
		t.Fatalf("fresh iat with an auth_time before the logout must be rejected")
	}
	assertSessionCleared(t, w)
}

// Verify without a project (unbound tunnel) has no access check to ride, so it
// asks the session endpoint. -007: the audience is host:<tunnel_host> from the
// verify URL (it used to come from X-Forwarded-Host + the shared cookie), and
// the session endpoint now also receives the credential's sids.
func TestVerify_NoProjectUsesSessionCheck(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	tunAud := "host:tun.example.com"
	tok, err := signSubsiteSession(revokeIdentity, tunAud, "sso-t", time.Now().Add(-time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/verify?tunnel_host=tun.example.com", nil)
	r.Header.Set("X-Forwarded-Host", "tun.example.com")
	r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code == http.StatusOK {
		t.Fatalf("revoked session passed project-less verify")
	}
	assertSessionCleared(t, w)
	calls := stub.callsTo(pathSessionRevoked)
	if len(calls) != 1 {
		t.Fatalf("session-check calls = %d, want 1", len(calls))
	}
	if calls[0].Get("sids") == "" {
		t.Errorf("session check carried no sids: %v", calls[0])
	}
}

// A session minted for another audience never authenticates here, and never
// reaches the revocation endpoint (-007 (new): per-aud isolation).
func TestVerify_WrongAudienceSessionRejectedWithoutChecks(t *testing.T) {
	stub := revokeEnv(t)
	other, err := signSubsiteSession(revokeIdentity, "project:aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "sso-o", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(other))
	if w.Code == http.StatusOK {
		t.Fatalf("another project's session passed verify")
	}
	if len(stub.calls) != 0 {
		t.Errorf("unauthenticated request reached muvee-server: %v", stub.calls)
	}
}

// -007 (new): password / phone sessions are project-bound with no users row
// behind them, so console logout never voids them (no session_iat is sent),
// but their own sid can still be revoked.
func TestVerify_ProjectSessionRevokedBySIDOnly(t *testing.T) {
	stub := revokeEnv(t)
	tok, err := signProjectSession("demo@acct", "Demo", "", "password", revokeProjectID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseFwdToken(tok, revokeAud, fwdKindSession)
	if err != nil || c.SID == "" {
		t.Fatalf("project session claims = %+v err=%v", c, err)
	}

	stub.revoked, stub.revokedIAT = true, time.Now().Add(time.Hour).Unix() // console logout "after" everything
	w := httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("console logout must not void a password session: status %d", w.Code)
	}
	for _, q := range stub.callsTo(pathSessionRevoked) {
		if q.Get("session_iat") != "" || q.Get("email") != "" {
			t.Errorf("password session check carried a console-logout key: %v", q)
		}
	}

	stub.revokeSID(c.SID)
	w = httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(tok))
	if w.Code == http.StatusOK {
		t.Fatalf("password session with a revoked sid must be rejected")
	}
	assertSessionCleared(t, w)
}

// A revoked session must not consume an invite on the user's behalf.
func TestVerify_RevokedSessionDoesNotConsumeInvite(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	r := revokeVerifyRequest(sessionAuthTimeAt(t, time.Now().Add(-time.Hour)))
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
	r := httptest.NewRequest(http.MethodGet, "/verify?tunnel_host=tun.example.com", nil)
	tok, err := signSubsiteSession(revokeIdentity, "host:tun.example.com", "sso-f", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

// kind=cli device-flow tokens are never checked against the browser-session
// logout time point (fwd_sessions_revoked_at); since BACKLOG-20261117-009 they
// have their own (cli_iat, see cli_revoke_test.go). A session-kind JWT
// replayed as Bearer is checked too. -007: an unmarked token (no kind claim,
// signed before kind existed) used to be treated like a session so a replayed
// pre-upgrade cookie could not dodge console logout; now parseFwdToken accepts
// only kind=session|cli, so it is refused outright (still ok=false, never
// admitted). New: a CLI token's own sid is revocable.
func TestVerify_BearerRevocationByKind(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	cli, _ := signCLIToken(revokeIdentity, revokeAud)
	cliRevoked, _ := signCLIToken(revokeIdentity, revokeAud)
	crc, _ := parseFwdToken(cliRevoked, revokeAud, fwdKindCLI)
	stub.revokeSID(crc.SID)
	legacyCLI := mustSign(t, &authClaims{
		Email: "a@corp.example", UserID: testUserID, Provider: "google",
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{revokeAud},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	session := sessionAuthTimeAt(t, time.Now().Add(-time.Hour))

	for name, tc := range map[string]struct {
		token string
		ok    bool
	}{
		"cli": {cli, true}, "cli-sid-revoked": {cliRevoked, false},
		"legacy-unmarked": {legacyCLI, false}, "session-as-bearer": {session, false},
	} {
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
	tok, err := signCLIToken(revokeIdentity, revokeAud)
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseFwdToken(tok, revokeAud, fwdKindCLI)
	if err != nil || c.Kind != fwdKindCLI || c.IssuedAt == nil || c.SID == "" {
		t.Errorf("cli token claims = %+v err=%v", c, err)
	}
	if _, err := parseFwdToken(tok, revokeAud, fwdKindSession); err == nil {
		t.Errorf("a cli token must not parse as a session")
	}
	src, err := os.ReadFile("authservice.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "token, err := signCLIToken(id, entry.Aud)") {
		t.Errorf("handleDeviceToken must sign kind=cli tokens via signCLIToken")
	}
}

// Every other consumer of resolveAuthClaims rejects a revoked session.
// -007: subsite endpoints take their audience from the X-Muvee-Aud header
// Traefik injects (the request Host must not be the central host), and
// /verify-admin from X-Forwarded-Host (host:traefik.<base>), so each case
// carries a session minted for its own audience.
func TestRevokedSession_OtherConsumers(t *testing.T) {
	stub := revokeEnv(t)
	adminEmails = map[string]struct{}{"a@corp.example": {}}
	t.Cleanup(func() { adminEmails = nil })
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()
	authTime := time.Now().Add(-time.Hour).Unix()

	withSession := func(method, target, host, aud string) *http.Request {
		tok, err := signSubsiteSession(revokeIdentity, aud, "sso-o", authTime)
		if err != nil {
			t.Fatal(err)
		}
		var r *http.Request
		if method == http.MethodPost {
			r = httptest.NewRequest(method, target, strings.NewReader("project_id="+revokeProjectID+"&reason=x"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, target, nil)
		}
		r.Host = host
		r.Header.Set("X-Forwarded-Host", host)
		r.Header.Set(routeAudienceHeader, aud)
		r.AddCookie(&http.Cookie{Name: fwdCookieName(fwdSessionCookieBase), Value: tok})
		return r
	}
	proj := func(method, target string) *http.Request {
		return withSession(method, target, "myproj.example.com", revokeAud)
	}
	admin := func() *http.Request {
		return withSession(http.MethodGet, "/verify-admin", "traefik.example.com", "host:traefik.example.com")
	}

	cases := []struct {
		name  string
		h     http.HandlerFunc
		req   *http.Request
		check func(*httptest.ResponseRecorder) bool
	}{
		{"verify-admin", handleVerifyAdmin, admin(),
			func(w *httptest.ResponseRecorder) bool { return w.Code != http.StatusOK }},
		{"userinfo", handleUserInfo, proj(http.MethodGet, "/_oauth/userinfo"),
			func(w *httptest.ResponseRecorder) bool { return w.Code == http.StatusUnauthorized }},
		{"request-access page", handleRequestAccessPage, proj(http.MethodGet, "/_oauth/request-access?project="+revokeProjectID),
			func(w *httptest.ResponseRecorder) bool {
				return w.Code == http.StatusFound && w.Header().Get("Location") == "/_oauth/login"
			}},
		{"request-access submit", handleRequestAccessSubmit, proj(http.MethodPost, "/_oauth/request-access"),
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
	handleUserInfo(w, proj(http.MethodGet, "/_oauth/userinfo"))
	if w.Code != http.StatusOK {
		t.Errorf("userinfo with a live session: status %d", w.Code)
	}
	w = httptest.NewRecorder()
	handleVerifyAdmin(w, admin())
	if w.Code != http.StatusOK {
		t.Errorf("verify-admin with a live session: status %d", w.Code)
	}
}

// Drift guard: resolveRequestClaims skips the revocation check, so only
// handleVerify (which runs it itself, piggybacking on the access check) may
// call it; everything else goes through resolveAuthClaims.
// -007: the call gained an aud argument (`resolveRequestClaims(r, aud)`), so
// the match is on the `resolveRequestClaims(r,` prefix — matching the old
// `(r)` text would have made this guard silently vacuous. It also now fails if
// it finds no call at all.
func TestResolveRequestClaimsOnlyInVerify(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
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
			if !strings.Contains(line, "resolveRequestClaims(r,") || strings.HasPrefix(line, "func ") {
				continue
			}
			calls++
			if !strings.HasPrefix(fn, "func handleVerify(") && !strings.HasPrefix(fn, "func resolveAuthClaims(") {
				t.Errorf("%s: %q calls resolveRequestClaims directly; use resolveAuthClaims", name, fn)
			}
		}
	}
	if calls < 2 {
		t.Errorf("found %d resolveRequestClaims calls, want >= 2 (handleVerify, resolveAuthClaims): the guard no longer matches the call shape", calls)
	}
}
