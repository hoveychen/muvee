package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sessionCookieFrom returns the __Host-muvee_fwd cookie set on the response.
func sessionCookieFrom(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == fwdCookieName(fwdSessionCookieBase) {
			return c
		}
	}
	return nil
}

// sessionExpiringIn mints a roaming subsite session for the verify project and
// rewrites its expiry to `left` from now, keeping every other claim.
func sessionExpiringIn(t *testing.T, left time.Duration) string {
	t.Helper()
	tok := mintTestSubsiteSession(t, testUserID, "a@corp.example", "A", "", "google", testVerifyProjectID)
	claims, err := parseFwdToken(tok, "project:"+testVerifyProjectID, fwdKindSession)
	if err != nil {
		t.Fatalf("parse minted session: %v", err)
	}
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(left))
	return mustSign(t, claims)
}

// TestHandleVerify_RenewsAgingSession pins the sliding session: a subsite
// session with less than subsiteSessionRenewAfter (6 h) left is re-signed with
// a full subsiteSessionLifetime (12 h) on a passing verify, keeping every claim
// (identity, provider, aud, sid, sso_sid, auth_time) intact.
// -007: 7-day session renewed in 4-day-left windows, cookie Domain=base ->
// 12 h session renewed once <6 h remain, host-only __Host- cookie (no Domain).
func TestHandleVerify_RenewsAgingSession(t *testing.T) {
	verifyEnv(t, true)

	session := sessionExpiringIn(t, 5*time.Hour)
	orig, _ := parseFwdToken(session, "project:"+testVerifyProjectID, fwdKindSession)
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, testVerifyProjectID, ""))

	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	c := sessionCookieFrom(w)
	if c == nil {
		t.Fatalf("aging session was not renewed")
	}
	if c.MaxAge != int(subsiteSessionLifetime.Seconds()) || c.Domain != "" || !c.HttpOnly || !c.Secure || c.Path != "/" {
		t.Errorf("renewed cookie attributes = MaxAge %d Domain %q HttpOnly %v Secure %v Path %q", c.MaxAge, c.Domain, c.HttpOnly, c.Secure, c.Path)
	}
	claims, err := parseFwdToken(c.Value, "project:"+testVerifyProjectID, fwdKindSession)
	if err != nil {
		t.Fatalf("renewed session does not parse for its own audience: %v", err)
	}
	if left := time.Until(claims.ExpiresAt.Time); left < subsiteSessionLifetime-time.Minute {
		t.Errorf("renewed session expires in %v, want ~%v", left, subsiteSessionLifetime)
	}
	if claims.UserID != testUserID || claims.Email != "a@corp.example" || claims.Provider != "google" {
		t.Errorf("renewal changed claims: %+v", claims)
	}
	if claims.SID != orig.SID || claims.SSOSID != orig.SSOSID || claims.AuthTime != orig.AuthTime {
		t.Errorf("renewal must keep sid/sso_sid/auth_time: got %q/%q/%d want %q/%q/%d",
			claims.SID, claims.SSOSID, claims.AuthTime, orig.SID, orig.SSOSID, orig.AuthTime)
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != "project:"+testVerifyProjectID {
		t.Errorf("renewal changed aud: %v", claims.Audience)
	}
}

// TestHandleVerify_FreshSessionNotRenewed keeps Set-Cookie off the common
// path: a session with more than subsiteSessionRenewAfter left is left alone —
// both a just-minted one (12 h left) and one right above the threshold.
// -007: "issued < sessionRenewAfter ago" with a 7-day lifetime -> 12 h session,
// threshold 6 h remaining.
func TestHandleVerify_FreshSessionNotRenewed(t *testing.T) {
	verifyEnv(t, true)

	cases := []struct {
		name    string
		session string
	}{
		{"just minted", mintTestSubsiteSession(t, testUserID, "a@corp.example", "A", "", "google", testVerifyProjectID)},
		{"7h left", sessionExpiringIn(t, 7*time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handleVerify(w, verifyRequest(tc.session, testVerifyProjectID, ""))
			if w.Code != http.StatusOK {
				t.Fatalf("verify status = %d, want 200", w.Code)
			}
			if c := sessionCookieFrom(w); c != nil {
				t.Errorf("fresh session must not be re-issued, got cookie MaxAge %d", c.MaxAge)
			}
		})
	}
}

// TestHandleVerify_ProjectSessionNotRenewed: password / phone sessions keep
// their fixed 7-day lifetime. verify never re-checks the demo account or phone
// grant behind them, so sliding would keep a removed grant alive forever.
// -007: aged to 2 days left (old 7-day threshold) -> also aged to 1 h left,
// well inside the subsite 6 h renew window: still never renewed.
func TestHandleVerify_ProjectSessionNotRenewed(t *testing.T) {
	verifyEnv(t, true)

	const projectID = testVerifyProjectID
	for _, left := range []time.Duration{2 * 24 * time.Hour, time.Hour} {
		session, err := signProjectSession("demo@corp.example", "Demo", "", "password", projectID)
		if err != nil {
			t.Fatalf("sign session: %v", err)
		}
		claims, err := parseFwdToken(session, "project:"+projectID, fwdKindSession)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(left))
		session = mustSign(t, claims)

		w := httptest.NewRecorder()
		handleVerify(w, verifyRequest(session, projectID, ""))
		if w.Code != http.StatusOK {
			t.Fatalf("verify status (left %v) = %d, want 200", left, w.Code)
		}
		if c := sessionCookieFrom(w); c != nil {
			t.Errorf("project-scoped session (left %v) must not slide, got renewed cookie", left)
		}
	}
}

// TestProjectSessionLifetimeFixed7d pins the fixed lifetime of a password /
// phone session as the signer actually issues it.
func TestProjectSessionLifetimeFixed7d(t *testing.T) {
	verifyEnv(t, true)
	session, err := signProjectSession("demo@corp.example", "Demo", "", "password", testVerifyProjectID)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := parseFwdToken(session, "project:"+testVerifyProjectID, fwdKindSession)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := time.Until(claims.ExpiresAt.Time); got < projectSessionLifetime-time.Minute || got > projectSessionLifetime {
		t.Errorf("project session expires in %v, want ~%v (7 days)", got, projectSessionLifetime)
	}
	if projectSessionLifetime != 7*24*time.Hour {
		t.Errorf("projectSessionLifetime = %v, want 7 days", projectSessionLifetime)
	}
}

// TestHandleVerify_BearerNotRenewed: the CLI's Bearer token (and a session
// presented as Bearer) has its own fixed lifetime; verify must not mint a
// browser cookie from it.
func TestHandleVerify_BearerNotRenewed(t *testing.T) {
	verifyEnv(t, true)

	aud := "project:" + testVerifyProjectID
	cli, err := signCLIToken(fwdIdentity{UserID: testUserID, Email: "a@corp.example", Name: "A", Provider: "google"}, aud)
	if err != nil {
		t.Fatalf("sign cli token: %v", err)
	}
	// A session inside the renew window, sent as Bearer instead of a cookie.
	aging := sessionExpiringIn(t, time.Hour)

	for name, token := range map[string]string{"cli token": cli, "aging session as bearer": aging} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/verify?project_id="+testVerifyProjectID, nil)
			r.Header.Set("X-Forwarded-Host", "myproj.example.com")
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			handleVerify(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("verify status = %d, want 200", w.Code)
			}
			if c := sessionCookieFrom(w); c != nil {
				t.Errorf("bearer auth must not set a session cookie")
			}
		})
	}
}

// TestHandleVerify_OtherAudienceSessionRejected is the isolation guarantee:
// a session minted for project B — even one that is valid, fresh, and aging
// inside the renew window — is not accepted on project A: verify redirects to
// login and neither renews nor re-issues any cookie.
// -007: sessions were shared across every project of the base domain -> a
// session is bound to one audience (project:<id> / host:<h>).
func TestHandleVerify_OtherAudienceSessionRejected(t *testing.T) {
	verifyEnv(t, true)

	const projectA = testVerifyProjectID
	const projectB = "33333333-3333-3333-3333-333333333333"

	roamingB := mintTestSubsiteSession(t, testUserID, "a@corp.example", "A", "", "google", projectB)
	agingB, err := signSubsiteSession(fwdIdentity{UserID: testUserID, Email: "a@corp.example", Provider: "google"},
		"project:"+projectB, "sso-sid-b", time.Now().Unix())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	cB, _ := parseFwdToken(agingB, "project:"+projectB, fwdKindSession)
	cB.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
	agingB = mustSign(t, cB)
	passwordB, err := signProjectSession("demo@corp.example", "Demo", "", "password", projectB)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tunnelHost := mintTestSubsiteSession(t, testUserID, "a@corp.example", "A", "", "google", "unused")
	// aud=host:<h> session (tunnel) presented on a project verify.
	tc, _ := parseFwdToken(tunnelHost, "project:unused", fwdKindSession)
	tc.Audience = jwt.ClaimStrings{"host:tun1"}
	tunnelHost = mustSign(t, tc)

	for name, session := range map[string]string{
		"roaming session of project B":  roamingB,
		"aging session of project B":    agingB,
		"password session of project B": passwordB,
		"tunnel host session":           tunnelHost,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handleVerify(w, verifyRequest(session, projectA, ""))
			if w.Code != http.StatusFound {
				t.Fatalf("verify status = %d, want 302 to login (body=%s)", w.Code, w.Body.String())
			}
			if c := sessionCookieFrom(w); c != nil && c.Value != "" {
				t.Errorf("a foreign-audience session must never be re-issued as a cookie")
			}
		})
	}

	// Sanity: project B's own verify admits the very same session.
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(roamingB, projectB, ""))
	if w.Code != http.StatusOK {
		t.Errorf("project B's own session on project B: status = %d, want 200", w.Code)
	}
}

// TestHandleVerify_LegacySessionCookieIgnored: the pre-isolation cookie name
// muvee_fwd_session (and a token with no aud) no longer authenticate.
// -007: muvee_fwd_session (Domain=base, no aud) accepted -> never accepted.
func TestHandleVerify_LegacySessionCookieIgnored(t *testing.T) {
	verifyEnv(t, true)

	session := mintTestSubsiteSession(t, testUserID, "a@corp.example", "A", "", "google", testVerifyProjectID)
	r := verifyRequest("", testVerifyProjectID, "")
	r.Header.Del("Cookie")
	r.AddCookie(&http.Cookie{Name: "muvee_fwd_session", Value: session})
	w := httptest.NewRecorder()
	handleVerify(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("legacy cookie name: status = %d, want 302", w.Code)
	}

	// Right cookie name, but the token carries no aud.
	claims, _ := parseFwdToken(session, "project:"+testVerifyProjectID, fwdKindSession)
	claims.Audience = nil
	w = httptest.NewRecorder()
	handleVerify(w, verifyRequest(mustSign(t, claims), testVerifyProjectID, ""))
	if w.Code != http.StatusFound {
		t.Fatalf("aud-less token: status = %d, want 302", w.Code)
	}
}

func mustSign(t *testing.T, c *authClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}
