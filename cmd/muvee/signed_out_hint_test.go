package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/auth"
)

func findCookie(cs []*http.Cookie, name string) *http.Cookie {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Logout drops a base-domain "signed out" hint so that a stale tab whose
// auth-guard reloads (→ ForwardAuth → /_oauth/login) does not get bounced
// straight through a single provider's still-live SSO session.
func TestFwdLogout_SetsSignedOutHint(t *testing.T) {
	withRedirectDomains(t)
	for host, wantDomain := range map[string]string{
		"datamap.sigma-fi.com": "sigma-fi.com",
		"proj.opencws.top":     "opencws.top",
	} {
		for _, q := range []string{"", "?redirect=/bye", "?redirect=https://evil.com/"} {
			c := findCookie(doLogout(t, host, q).Cookies(), signedOutHintCookie)
			if c == nil {
				t.Fatalf("%s%s: no %s cookie", host, q, signedOutHintCookie)
			}
			if c.Value == "" || c.MaxAge < 7*24*3600 || c.Domain != wantDomain || c.Path != "/" ||
				!c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("%s%s: bad hint cookie %+v", host, q, c)
			}
		}
	}
}

func withSingleGoogleProject(t *testing.T) {
	t.Helper()
	withRedirectDomains(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id": "p1", "project_name": "datamap", "enabled_providers": "google",
		})
	}))
	t.Cleanup(upstream.Close)
	prevServer := muveeServerURL
	muveeServerURL = upstream.URL
	t.Cleanup(func() { muveeServerURL = prevServer })
	setProvidersForTest(t, "google")
}

func doLoginPage(t *testing.T, hint bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "/_oauth/login", nil)
	r.Host = "datamap.sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	if hint {
		r.AddCookie(&http.Cookie{Name: signedOutHintCookie, Value: "1"})
	}
	w := httptest.NewRecorder()
	handleLoginPage(w, r)
	return w
}

// Regression guard: without the hint, a single-provider project keeps
// auto-redirecting to the provider exactly as before.
func TestLoginPage_SingleProviderAutoRedirectsWithoutHint(t *testing.T) {
	withSingleGoogleProject(t)
	w := doLoginPage(t, false)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/_oauth/login?provider=google" {
		t.Fatalf("status=%d Location=%q, want 302 to provider=google", w.Code, w.Header().Get("Location"))
	}
}

func TestLoginPage_SignedOutHintShowsSelectionPage(t *testing.T) {
	withSingleGoogleProject(t)
	w := doLoginPage(t, true)
	if w.Code != http.StatusOK || w.Header().Get("Location") != "" {
		t.Fatalf("status=%d Location=%q, want rendered 200", w.Code, w.Header().Get("Location"))
	}
	if !strings.Contains(w.Body.String(), `href="/_oauth/login?provider=google"`) {
		t.Fatal("selection page missing the provider button")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", w.Header().Get("Cache-Control"))
	}
}

// A completed sign-in clears the hint so later session expiries go back to
// the seamless single-provider redirect.
func TestPasswordLogin_ClearsSignedOutHint(t *testing.T) {
	withRedirectDomains(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/internal/projects/by-host") {
			_ = json.NewEncoder(w).Encode(map[string]any{"project_id": "p1", "password_login": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "email": "u@example.com", "name": "u", "user_id": "u1"})
	}))
	defer upstream.Close()
	prevServer, prevSecret := muveeServerURL, jwtSecret
	muveeServerURL = upstream.URL
	jwtSecret = []byte("test-secret")
	t.Cleanup(func() { muveeServerURL, jwtSecret = prevServer, prevSecret })

	r := httptest.NewRequest("POST", "/_oauth/password", strings.NewReader("username=u&password=p"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Host = "datamap.sigma-fi.com"
	r.Header.Set("X-Forwarded-Host", "datamap.sigma-fi.com")
	r.AddCookie(&http.Cookie{Name: signedOutHintCookie, Value: "1"})
	w := httptest.NewRecorder()
	handlePasswordLogin(w, r)
	res := w.Result()
	if findCookie(res.Cookies(), "muvee_fwd_session") == nil {
		t.Fatalf("password login did not issue a session (status %d)", res.StatusCode)
	}
	c := findCookie(res.Cookies(), signedOutHintCookie)
	if c == nil || c.MaxAge >= 0 || c.Domain != "sigma-fi.com" || c.Path != "/" {
		t.Fatalf("hint not cleared on the base domain: %+v", c)
	}
}

// Drift guard: every sign-in that issues a fresh muvee_fwd_session must go
// through issueFwdSession (which also clears the signed-out hint). Only the
// silent renewal of an already-valid session may write the cookie directly.
func TestFwdSessionIssuedOnlyViaIssueFwdSession(t *testing.T) {
	for _, file := range []string{"authservice.go", "login_token.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		fn := ""
		for i, line := range lines {
			if strings.HasPrefix(line, "func ") {
				fn = line
			}
			if !strings.Contains(line, `"muvee_fwd_session"`) || !strings.Contains(line, "Name:") {
				continue
			}
			// Look at this cookie literal's Value (same or next line).
			lit := line
			if i+1 < len(lines) {
				lit += lines[i+1]
			}
			if strings.Contains(lit, `Value: ""`) || strings.Contains(lit, `Value:    ""`) {
				continue // clearing, not issuing
			}
			switch {
			case strings.HasPrefix(fn, "func issueFwdSession("),
				strings.HasPrefix(fn, "func renewSessionCookie("):
			default:
				t.Errorf("%s: muvee_fwd_session issued directly in %q; use issueFwdSession", file, fn)
			}
		}
	}
}

// setProvidersForTest installs a real Google provider (dummy credentials) as
// the global forward-auth provider set for the duration of the test.
func setProvidersForTest(t *testing.T, _ string) {
	t.Helper()
	t.Setenv("GOOGLE_CLIENT_ID", "cid_test")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret_test")
	provs, err := auth.NewForwardAuthProviders(forwardAuthBase)
	if err != nil {
		t.Fatalf("build providers: %v", err)
	}
	only := map[string]auth.Provider{"google": provs["google"]}
	if only["google"] == nil {
		t.Fatalf("google provider not built; got %v", provs)
	}
	prev := fwdProvidersAtomic.Load()
	fwdProvidersAtomic.Store(&only)
	t.Cleanup(func() { fwdProvidersAtomic.Store(prev) })
}
