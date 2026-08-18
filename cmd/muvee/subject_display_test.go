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

// TestLoginTokenPoll_ReturnsUserID pins the SDK-facing side: the polled result
// carries the user id, which for an email-less identity is the only stable
// handle the SDK can key its own state on.
func TestLoginTokenPoll_ReturnsUserID(t *testing.T) {
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

	var out struct {
		Status string            `json:"status"`
		User   map[string]string `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode poll response: %v (body=%s)", err, w.Body.String())
	}
	if out.Status != "success" {
		t.Fatalf("status = %q, want success (body=%s)", out.Status, w.Body.String())
	}
	if out.User["user_id"] != testUserID {
		t.Errorf("poll user.user_id = %q, want %q", out.User["user_id"], testUserID)
	}
}
