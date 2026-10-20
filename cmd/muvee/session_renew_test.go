package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// sessionCookieFrom returns the muvee_fwd_session cookie set on the response.
func sessionCookieFrom(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "muvee_fwd_session" {
			return c
		}
	}
	return nil
}

// TestHandleVerify_RenewsAgingSession pins the sliding session: a session
// older than sessionRenewAfter is re-signed with a full lifetime on a passing
// verify, keeping every claim (identity, provider, project binding) intact.
func TestHandleVerify_RenewsAgingSession(t *testing.T) {
	verifyEnv(t, true)

	session, err := signForwardJWTWithExpiry(testUserID, "a@corp.example", "A", "", "google", 3*24*time.Hour)
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	c := sessionCookieFrom(w)
	if c == nil {
		t.Fatalf("aging session was not renewed")
	}
	if c.MaxAge != int(sessionLifetime.Seconds()) || c.Domain != "example.com" || !c.HttpOnly {
		t.Errorf("renewed cookie attributes = MaxAge %d Domain %q HttpOnly %v", c.MaxAge, c.Domain, c.HttpOnly)
	}
	claims, err := parseForwardJWT(c.Value)
	if err != nil {
		t.Fatalf("renewed session does not parse: %v", err)
	}
	if left := time.Until(claims.ExpiresAt.Time); left < sessionLifetime-time.Minute {
		t.Errorf("renewed session expires in %v, want ~%v", left, sessionLifetime)
	}
	if claims.UserID != testUserID || claims.Email != "a@corp.example" || claims.Provider != "google" {
		t.Errorf("renewal changed claims: %+v", claims)
	}
}

// TestHandleVerify_FreshSessionNotRenewed keeps Set-Cookie off the common
// path: a session issued less than sessionRenewAfter ago is left alone.
func TestHandleVerify_FreshSessionNotRenewed(t *testing.T) {
	verifyEnv(t, true)

	session, err := signForwardJWT(testUserID, "a@corp.example", "A", "", "google")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200", w.Code)
	}
	if c := sessionCookieFrom(w); c != nil {
		t.Errorf("fresh session must not be re-issued, got cookie MaxAge %d", c.MaxAge)
	}
}

// TestHandleVerify_ProjectSessionNotRenewed: password / phone sessions keep
// their fixed lifetime. verify never re-checks the demo account or phone grant
// behind them, so sliding would keep a removed grant alive forever.
func TestHandleVerify_ProjectSessionNotRenewed(t *testing.T) {
	verifyEnv(t, true)

	const projectID = "11111111-1111-1111-1111-111111111111"
	session, err := signForwardPasswordJWT("demo@corp.example", "Demo", "", projectID)
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	// Age it well past the renew threshold.
	claims, _ := parseForwardJWT(session)
	claims.ExpiresAt.Time = time.Now().Add(2 * 24 * time.Hour)
	session = mustSign(t, claims)

	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, projectID, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200", w.Code)
	}
	if c := sessionCookieFrom(w); c != nil {
		t.Errorf("project-scoped session must not slide, got renewed cookie")
	}
}

// TestHandleVerify_BearerNotRenewed: the CLI's Bearer JWT has its own fixed
// lifetime; verify must not mint a browser cookie from it.
func TestHandleVerify_BearerNotRenewed(t *testing.T) {
	verifyEnv(t, true)

	token, err := signForwardJWTWithExpiry(testUserID, "a@corp.example", "A", "", "google", time.Hour)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/verify?project_id=11111111-1111-1111-1111-111111111111", nil)
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
}

func mustSign(t *testing.T, c *authClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}
