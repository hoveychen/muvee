package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// verifyEnv points authservice at a stub muvee-server that records the access
// check it receives and answers with allow. Returns the recorded query.
func verifyEnv(t *testing.T, allowed bool) *url.Values {
	t.Helper()
	got := &url.Values{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if allowed {
			_, _ = w.Write([]byte(`{"allowed":true,"mode":"public"}`))
		} else {
			_, _ = w.Write([]byte(`{"allowed":false,"mode":"private"}`))
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
	return got
}

// verifyRequest builds the ForwardAuth request Traefik would send for a
// project, carrying session as the muvee_fwd_session cookie.
func verifyRequest(session, projectID, domains string) *http.Request {
	q := url.Values{}
	q.Set("project_id", projectID)
	if domains != "" {
		q.Set("domains", domains)
	}
	r := httptest.NewRequest(http.MethodGet, "/verify?"+q.Encode(), nil)
	r.Header.Set("X-Forwarded-Host", "myproj.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: "muvee_fwd_session", Value: session})
	return r
}

const testUserID = "22222222-2222-2222-2222-222222222222"

// TestHandleVerify_EmailLessSessionChecksAccessByUserID pins the second half
// of the subject-keyed login: a session with no email must still reach the
// per-project access check, keyed on the user id, and must hand downstream a
// usable identity. Keying the check on an empty email would have the server
// look up a user that cannot exist, i.e. deny every request.
func TestHandleVerify_EmailLessSessionChecksAccessByUserID(t *testing.T) {
	gotQuery := verifyEnv(t, true)

	session, err := signForwardJWT(testUserID, "", "Fake User", "https://cdn.example/a.png", "twitter")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if got := gotQuery.Get("user_id"); got != testUserID {
		t.Errorf("access check user_id = %q, want %q", got, testUserID)
	}
	if got := gotQuery.Get("email"); got != "" {
		t.Errorf("access check email = %q, want it omitted for an email-less identity", got)
	}
	if got := w.Header().Get("X-Forwarded-User-Id"); got != testUserID {
		t.Errorf("X-Forwarded-User-Id = %q, want %q", got, testUserID)
	}
	if got := w.Header().Get("X-Forwarded-User"); got != testUserID {
		t.Errorf("X-Forwarded-User = %q, want the user id as fallback when there is no email", got)
	}
}

// TestHandleVerify_EmailLessSessionSkipsDomainWhitelist covers the admission
// decision this plan settled on: auth_allowed_domains cannot match an identity
// with no address, so the whitelist is skipped rather than treated as a deny —
// admission is then entirely up to the project ACL.
func TestHandleVerify_EmailLessSessionSkipsDomainWhitelist(t *testing.T) {
	gotQuery := verifyEnv(t, true)

	session, err := signForwardJWT(testUserID, "", "Fake User", "", "twitter")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", "corp.example"))

	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200 — the domain whitelist must not reject an address-less identity outright (body=%s)", w.Code, w.Body.String())
	}
	if gotQuery.Get("user_id") != testUserID {
		t.Errorf("the ACL check must still run: user_id = %q", gotQuery.Get("user_id"))
	}
}

// TestHandleVerify_EmailLessSessionDeniedWithoutGrant is the other side of the
// same decision: skipping the whitelist must not turn into a free pass. When
// the project ACL says no, the user is bounced to request-access exactly like
// an email-bearing stranger.
func TestHandleVerify_EmailLessSessionDeniedWithoutGrant(t *testing.T) {
	verifyEnv(t, false)

	session, err := signForwardJWT(testUserID, "", "Fake User", "", "twitter")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", "corp.example"))

	if w.Code != http.StatusFound && w.Code != http.StatusForbidden {
		t.Fatalf("verify status = %d, want a redirect to request-access or 403 (body=%s)", w.Code, w.Body.String())
	}
}

// TestHandleVerify_EmailSessionStillKeysOnEmail guards the untouched majority
// path: an email-bearing session keeps being checked by email, keeps matching
// the domain whitelist, and keeps seeing its address in X-Forwarded-User.
func TestHandleVerify_EmailSessionStillKeysOnEmail(t *testing.T) {
	gotQuery := verifyEnv(t, true)

	session, err := signForwardJWT(testUserID, "alice@corp.example", "Alice", "", "google")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", "corp.example"))

	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if got := gotQuery.Get("email"); got != "alice@corp.example" {
		t.Errorf("access check email = %q, want alice@corp.example", got)
	}
	if got := gotQuery.Get("user_id"); got != "" {
		t.Errorf("access check user_id = %q, want it omitted when an email is present", got)
	}
	if got := w.Header().Get("X-Forwarded-User"); got != "alice@corp.example" {
		t.Errorf("X-Forwarded-User = %q, want the email", got)
	}
	if got := w.Header().Get("X-Forwarded-User-Id"); got != testUserID {
		t.Errorf("X-Forwarded-User-Id = %q, want it populated for email users too", got)
	}
}

// TestHandleVerify_EmailSessionWrongDomainStillRejected pins that the
// whitelist skip is conditioned on the *absence* of an email, not loosened for
// everyone.
func TestHandleVerify_EmailSessionWrongDomainStillRejected(t *testing.T) {
	verifyEnv(t, true)

	session, err := signForwardJWT(testUserID, "mallory@evil.example", "Mallory", "", "google")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", "corp.example"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("verify status = %d, want 403 for an out-of-domain email (body=%s)", w.Code, w.Body.String())
	}
}

// TestHandleVerify_KeylessSessionFailsClosed covers the degenerate claim that
// carries neither key: it must go back to login, never through the checks on
// empty strings.
func TestHandleVerify_KeylessSessionFailsClosed(t *testing.T) {
	verifyEnv(t, true)

	session, err := signForwardJWT("", "", "Nobody", "", "twitter")
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	w := httptest.NewRecorder()
	handleVerify(w, verifyRequest(session, "11111111-1111-1111-1111-111111111111", ""))

	if w.Code != http.StatusFound {
		t.Fatalf("verify status = %d, want a redirect back to login (body=%s)", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc == "" {
		t.Error("expected a Location header pointing at the login page")
	}
}
