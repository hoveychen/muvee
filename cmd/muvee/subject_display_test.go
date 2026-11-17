package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClaimsAccountLabel_FallsBackWhenThereIsNoEmail pins the display chain the
// auth pages print after "Signed in as": address, then the IdP's display name,
// then the user id — which always exists.
func TestClaimsAccountLabel_FallsBackWhenThereIsNoEmail(t *testing.T) {
	cases := []struct {
		name   string
		claims authClaims
		want   string
	}{
		{"email wins", authClaims{Email: "alice@corp.example", Name: "Alice", UserID: testUserID}, "alice@corp.example"},
		{"name when no email", authClaims{Name: "Fake User", UserID: testUserID}, "Fake User"},
		{"user id as last resort", authClaims{UserID: testUserID}, testUserID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claimsAccountLabel(&c.claims); got != c.want {
				t.Errorf("claimsAccountLabel = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRequestAccessPage_RendersAccountLabel keeps the template and the data map
// from drifting apart: the page must print whatever label the handler computed,
// not an empty string where an email used to be.
func TestRequestAccessPage_RendersAccountLabel(t *testing.T) {
	langs := []struct{ header, want string }{
		{"en", "Signed in as Fake User"},
		{"zh-CN", "已登录为 Fake User"},
	}
	for _, phase := range []string{"form", "submitted", "already-allowed"} {
		for _, lang := range langs {
			t.Run(phase+"/"+lang.header, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/_oauth/request-access?project=p", nil)
				r.Header.Set("Accept-Language", lang.header)
				w := httptest.NewRecorder()
				renderRequestAccessPage(w, r, http.StatusOK, map[string]string{
					"Phase":       phase,
					"ProjectID":   "11111111-1111-1111-1111-111111111111",
					"ProjectName": "Demo",
					"Account":     "Fake User",
				})
				if body := w.Body.String(); !strings.Contains(body, lang.want) {
					t.Errorf("request-access page (%s, %s) does not show %q; body=%s", phase, lang.header, lang.want, body)
				}
			})
		}
	}
}

// TestLoginTokenPoll_ReturnsStatusOnly pins the SDK-facing side of the poll.
// -007: the polled result carried the user id / identity (user.user_id) ->
// status only: the login_token travels in a provider URL, so handing out an
// identity to whoever polls it would let a lured victim's identity be read
// back. Browser sign-in uses the popup flow instead.
func TestLoginTokenPoll_ReturnsStatusOnly(t *testing.T) {
	token := "tok-" + t.Name()
	t.Cleanup(func() { loginTokens.Delete(token) })
	loginTokens.Store(token, &loginTokenEntry{
		Provider:     "twitter",
		ProviderName: "twitter",
		Status:       "success",
		Email:        "",
		UserID:       testUserID,
		Name:         "Fake User",
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	})

	r := httptest.NewRequest(http.MethodPost, "/_oauth/login-token/poll",
		strings.NewReader(`{"login_token":"`+token+`"}`))
	w := httptest.NewRecorder()
	handleLoginTokenPoll(w, r)

	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode poll response: %v (body=%s)", err, w.Body.String())
	}
	if out["status"] != "success" {
		t.Fatalf("status = %v, want success (body=%s)", out["status"], w.Body.String())
	}
	if _, has := out["user"]; has {
		t.Errorf("poll must not return an identity, got user=%v", out["user"])
	}
	for _, leak := range []string{testUserID, "Fake User", "twitter"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("poll body leaks %q: %s", leak, w.Body.String())
		}
	}
	// Single-use: the token is consumed by the successful poll.
	if _, still := loginTokens.Load(token); still {
		t.Error("login_token must be consumed by a successful poll")
	}
}
