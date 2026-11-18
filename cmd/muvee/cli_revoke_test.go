package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// BACKLOG-20261117-009: console logout (and the console's "revoke all CLI
// tokens" button) voids the user's kind=cli device-flow tokens through
// users.cli_tokens_revoked_at. authservice sends a CLI token's auth_time as
// cli_iat — never as session_iat, which is compared with
// fwd_sessions_revoked_at — and answers a revoked CLI token with a 401 JSON
// body pointing at the device flow instead of the browser login redirect.

const cliRevokeHost = "site-a.test"

// cliVerifyRequest is a ForwardAuth request for the revoke project carrying
// tok as a Bearer token.
func cliVerifyRequest(target, tok string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("X-Forwarded-Host", cliRevokeHost)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

// cliTokenIssuedAt signs a CLI token whose auth_time (and iat) is at.
func cliTokenIssuedAt(t *testing.T, aud string, at time.Time) string {
	t.Helper()
	tok, err := signCLIToken(revokeIdentity, aud)
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseFwdToken(tok, aud, fwdKindCLI)
	if err != nil {
		t.Fatal(err)
	}
	c.AuthTime = at.Unix()
	return mustSign(t, c)
}

// assertCLIRevokedResponse checks the 401 JSON a revoked CLI token gets: no
// login redirect, no cookie operations, a WWW-Authenticate challenge, and the
// device-flow URL of the subsite it was presented to.
func assertCLIRevokedResponse(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked CLI token: status %d (location %q), want 401", w.Code, w.Header().Get("Location"))
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("revoked CLI token was redirected to %q", loc)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if wa := w.Header().Get("WWW-Authenticate"); !strings.Contains(wa, `Bearer`) || !strings.Contains(wa, `error="invalid_token"`) {
		t.Errorf("WWW-Authenticate = %q, want a Bearer invalid_token challenge", wa)
	}
	if cs := w.Result().Cookies(); len(cs) != 0 {
		t.Errorf("revoked CLI token response touched cookies: %+v", cs)
	}
	var body struct {
		Error         string `json:"error"`
		Message       string `json:"message"`
		DeviceCodeURL string `json:"device_code_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, w.Body.String())
	}
	wantURL := "https://" + cliRevokeHost + "/_oauth/device/code"
	if body.Error != "token_revoked" || body.DeviceCodeURL != wantURL || !strings.Contains(body.Message, wantURL) {
		t.Errorf("body = %+v, want error=token_revoked and the device-flow URL %q", body, wantURL)
	}
	if w.Header().Get("X-Forwarded-User") != "" {
		t.Errorf("revoked CLI token leaked identity headers")
	}
}

// Path 1: a project verify answers revocation in the access check itself.
func TestCLIRevoke_AccessCheckPath(t *testing.T) {
	stub := revokeEnv(t)
	revokedAt := time.Now().Add(-time.Hour)
	stub.cliRevoked, stub.cliRevokedIAT = true, revokedAt.Unix()

	before := cliTokenIssuedAt(t, revokeAud, revokedAt.Add(-time.Minute))
	w := httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?project_id="+revokeProjectID, before))
	assertCLIRevokedResponse(t, w)

	checks := stub.callsTo(pathAccessCheck)
	if len(checks) != 1 {
		t.Fatalf("access check calls = %d, want 1", len(checks))
	}
	if checks[0].Get("session_iat") != "" {
		t.Errorf("CLI token sent session_iat (compared with fwd_sessions_revoked_at): %v", checks[0])
	}
	if got := checks[0].Get("cli_iat"); got != strconv.FormatInt(revokedAt.Add(-time.Minute).Unix(), 10) {
		t.Errorf("cli_iat = %q, want the token's auth_time", got)
	}
	if n := len(stub.callsTo(pathSessionRevoked)); n != 0 {
		t.Errorf("project verify made %d extra session-check calls, want 0", n)
	}

	// Same-second boundary: issued in the revocation's second counts as revoked.
	same := cliTokenIssuedAt(t, revokeAud, time.Unix(revokedAt.Unix(), 0))
	w = httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?project_id="+revokeProjectID, same))
	assertCLIRevokedResponse(t, w)

	// A token from a device flow completed after the revocation works.
	after := cliTokenIssuedAt(t, revokeAud, revokedAt.Add(time.Minute))
	w = httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?project_id="+revokeProjectID, after))
	if w.Code != http.StatusOK {
		t.Fatalf("CLI token issued after the revocation: status %d, want 200", w.Code)
	}
}

// Path 2: verify without a project (unbound tunnel) and /_oauth/userinfo ask
// the standalone session check. The CLI identity rides cli_email / cli_user_id
// so a server that predates cli_iat (which requires session_iat alongside
// email / user_id) still answers 200 instead of 400.
func TestCLIRevoke_SessionCheckPath(t *testing.T) {
	stub := revokeEnv(t)
	revokedAt := time.Now().Add(-time.Hour)
	stub.cliRevoked, stub.cliRevokedIAT = true, revokedAt.Unix()

	tunAud := "host:" + cliRevokeHost
	before := cliTokenIssuedAt(t, tunAud, revokedAt.Add(-time.Minute))
	w := httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?tunnel_host="+cliRevokeHost, before))
	assertCLIRevokedResponse(t, w)

	calls := stub.callsTo(pathSessionRevoked)
	if len(calls) != 1 {
		t.Fatalf("session-check calls = %d, want 1", len(calls))
	}
	q := calls[0]
	if q.Get("email") != "" || q.Get("user_id") != "" || q.Get("session_iat") != "" {
		t.Errorf("CLI session check carried browser-session keys: %v", q)
	}
	if q.Get("cli_email") != revokeIdentity.Email || q.Get("cli_iat") == "" || q.Get("sids") == "" {
		t.Errorf("CLI session check = %v, want cli_email, cli_iat and sids", q)
	}

	// /_oauth/userinfo goes through resolveAuthClaims.
	ui := cliVerifyRequest("/_oauth/userinfo", cliTokenIssuedAt(t, revokeAud, revokedAt.Add(-time.Minute)))
	ui.Header.Set(routeAudienceHeader, revokeAud)
	ui.Host = cliRevokeHost
	w = httptest.NewRecorder()
	handleUserInfo(w, ui)
	assertCLIRevokedResponse(t, w)

	after := cliTokenIssuedAt(t, tunAud, revokedAt.Add(time.Minute))
	w = httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?tunnel_host="+cliRevokeHost, after))
	if w.Code != http.StatusOK {
		t.Fatalf("CLI token issued after the revocation: status %d, want 200", w.Code)
	}
}

// An email-less identity sends cli_user_id.
func TestCLIRevoke_EmailLessIdentity(t *testing.T) {
	stub := revokeEnv(t)
	id := fwdIdentity{UserID: testUserID, Name: "X", Provider: "twitter"}
	tunAud := "host:" + cliRevokeHost
	tok, err := signCLIToken(id, tunAud)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?tunnel_host="+cliRevokeHost, tok))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	q := stub.callsTo(pathSessionRevoked)[0]
	if q.Get("cli_user_id") != testUserID || q.Get("cli_email") != "" || q.Get("user_id") != "" {
		t.Errorf("email-less CLI session check = %v, want cli_user_id only", q)
	}
}

// The two revocation time points are independent: a console-logout stamp on
// fwd_sessions_revoked_at alone (every account that signed out before the
// upgrade) leaves CLI tokens working, and a CLI-only revocation leaves the
// browser session cookie working.
func TestCLIRevoke_IndependentOfSessionRevocation(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()

	cli := cliTokenIssuedAt(t, revokeAud, time.Now().Add(-time.Hour))
	w := httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?project_id="+revokeProjectID, cli))
	if w.Code != http.StatusOK {
		t.Fatalf("CLI token voided by a session-only revocation: status %d", w.Code)
	}

	stub.revoked = false
	stub.cliRevoked, stub.cliRevokedIAT = true, time.Now().Unix()
	w = httptest.NewRecorder()
	handleVerify(w, revokeVerifyRequest(sessionAuthTimeAt(t, time.Now().Add(-time.Hour))))
	if w.Code != http.StatusOK {
		t.Fatalf("browser session voided by a CLI-only revocation: status %d", w.Code)
	}
}

// A revoked browser session keeps the 302-to-login answer, also when replayed
// as a Bearer header; only kind=cli gets the 401 JSON.
func TestCLIRevoke_SessionKindKeepsRedirect(t *testing.T) {
	stub := revokeEnv(t)
	stub.revoked, stub.revokedIAT = true, time.Now().Unix()
	stub.cliRevoked, stub.cliRevokedIAT = true, time.Now().Unix()

	session := sessionAuthTimeAt(t, time.Now().Add(-time.Hour))
	r := revokeVerifyRequest(session)
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code != http.StatusFound || !strings.HasSuffix(w.Header().Get("Location"), "/_oauth/login") {
		t.Fatalf("revoked cookie: status %d location %q, want 302 to login", w.Code, w.Header().Get("Location"))
	}
	assertSessionCleared(t, w)

	r = cliVerifyRequest("/verify?project_id="+revokeProjectID, session)
	r.Header.Set("Sec-Fetch-Dest", "document")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w = httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("revoked session replayed as Bearer: status %d, want the 302 a session gets", w.Code)
	}
}

// Per-session sid revocation of a CLI token also answers 401 JSON.
func TestCLIRevoke_SIDRevokedIs401(t *testing.T) {
	stub := revokeEnv(t)
	tok := cliTokenIssuedAt(t, revokeAud, time.Now())
	c, _ := parseFwdToken(tok, revokeAud, fwdKindCLI)
	stub.revokeSID(c.SID)
	w := httptest.NewRecorder()
	handleVerify(w, cliVerifyRequest("/verify?project_id="+revokeProjectID, tok))
	assertCLIRevokedResponse(t, w)
}

// Every consumer of resolveAuthClaims answers a revoked CLI token with the
// same 401 JSON (not the login redirect a revoked session gets).
func TestCLIRevoke_OtherConsumers(t *testing.T) {
	stub := revokeEnv(t)
	adminEmails = map[string]struct{}{revokeIdentity.Email: {}}
	t.Cleanup(func() { adminEmails = nil })
	prevCookie := cookieDomain
	cookieDomain = "site-a.test" // the dashboard host is traefik.<base>
	t.Cleanup(func() { cookieDomain = prevCookie })
	revokedAt := time.Now()
	stub.cliRevoked, stub.cliRevokedIAT = true, revokedAt.Unix()

	req := func(method, target, aud string) *http.Request {
		r := cliVerifyRequest(target, cliTokenIssuedAt(t, aud, revokedAt.Add(-time.Hour)))
		r.Method = method
		r.Host = cliRevokeHost
		r.Header.Set(routeAudienceHeader, aud)
		return r
	}
	adminReq := cliVerifyRequest("/verify-admin", cliTokenIssuedAt(t, "host:traefik.site-a.test", revokedAt.Add(-time.Hour)))
	adminReq.Header.Set("X-Forwarded-Host", "traefik.site-a.test")

	for name, tc := range map[string]struct {
		h http.HandlerFunc
		r *http.Request
	}{
		"verify-admin":          {handleVerifyAdmin, adminReq},
		"userinfo":              {handleUserInfo, req(http.MethodGet, "/_oauth/userinfo", revokeAud)},
		"request-access page":   {handleRequestAccessPage, req(http.MethodGet, "/_oauth/request-access?project="+revokeProjectID, revokeAud)},
		"request-access submit": {handleRequestAccessSubmit, req(http.MethodPost, "/_oauth/request-access", revokeAud)},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.h(w, tc.r)
			if name == "verify-admin" {
				// Presented on traefik.<base>: the device URL names that host.
				if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "https://traefik.site-a.test/_oauth/device/code") {
					t.Fatalf("status %d body %q", w.Code, w.Body.String())
				}
				return
			}
			assertCLIRevokedResponse(t, w)
		})
	}
}
