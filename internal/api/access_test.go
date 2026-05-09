package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalAPIKey_Deterministic(t *testing.T) {
	t.Setenv("JWT_SECRET", "secret-a")
	a1 := internalAPIKey()
	a2 := internalAPIKey()
	if a1 != a2 {
		t.Fatalf("expected deterministic key for same secret, got %q vs %q", a1, a2)
	}
	t.Setenv("JWT_SECRET", "secret-b")
	b := internalAPIKey()
	if a1 == b {
		t.Fatalf("expected different keys for different secrets, got %q == %q", a1, b)
	}
}

func TestInternalAPIKey_NonEmptyEvenWithEmptySecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	if k := internalAPIKey(); k == "" {
		t.Fatal("expected non-empty key (sha256 of empty string is still hex-encoded)")
	}
}

func TestHandleInternalAccessCheck_RejectsMissingOrWrongKey(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	s := &Server{}

	cases := []struct {
		name       string
		key        string
		wantStatus int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong key", "deadbeef", http.StatusUnauthorized},
	}
	const validParams = "?project_id=11111111-1111-1111-1111-111111111111&email=u%40x.com"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/internal/access/check"+validParams, nil)
			if c.key != "" {
				r.Header.Set("X-Muvee-Internal-Key", c.key)
			}
			w := httptest.NewRecorder()
			s.handleInternalAccessCheck(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, c.wantStatus)
			}
		})
	}
}

func TestHandleInternalAccessCheck_RejectsBadParams(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	key := internalAPIKey()
	s := &Server{}

	cases := []struct {
		name, query string
		wantStatus  int
	}{
		{"missing project_id", "?email=u@x.com", http.StatusBadRequest},
		{"missing email", "?project_id=11111111-1111-1111-1111-111111111111", http.StatusBadRequest},
		{"invalid project_id", "?project_id=not-a-uuid&email=u@x.com", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/internal/access/check"+c.query, nil)
			r.Header.Set("X-Muvee-Internal-Key", key)
			w := httptest.NewRecorder()
			s.handleInternalAccessCheck(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("got status %d, want %d (body=%s)", w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}

func TestHandleInternalAuthUpsert_RejectsMissingOrWrongKey(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	s := &Server{}

	cases := []struct {
		name       string
		key        string
		wantStatus int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong key", "deadbeef", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/upsert",
				strings.NewReader(`{"email":"a@b.com","provider":"feishu"}`))
			if c.key != "" {
				r.Header.Set("X-Muvee-Internal-Key", c.key)
			}
			w := httptest.NewRecorder()
			s.handleInternalAuthUpsert(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, c.wantStatus)
			}
		})
	}
}

func TestHandleInternalAuthUpsert_RejectsBadPayload(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	key := internalAPIKey()
	s := &Server{}

	cases := []struct {
		name, body string
		wantStatus int
	}{
		{"invalid json", `{not json`, http.StatusBadRequest},
		{"missing email", `{"provider":"feishu"}`, http.StatusBadRequest},
		{"missing provider", `{"email":"a@b.com"}`, http.StatusBadRequest},
		{"empty body", `{}`, http.StatusBadRequest},
		{"whitespace email", `{"email":"   ","provider":"feishu"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/upsert",
				strings.NewReader(c.body))
			r.Header.Set("X-Muvee-Internal-Key", key)
			w := httptest.NewRecorder()
			s.handleInternalAuthUpsert(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("got status %d, want %d (body=%s)", w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}

func TestHandleInternalAuthIdentityUpsert_RejectsMissingOrWrongKey(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	s := &Server{}

	cases := []struct {
		name       string
		key        string
		wantStatus int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong key", "deadbeef", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/identity-upsert",
				strings.NewReader(`{"email":"a@b.com"}`))
			if c.key != "" {
				r.Header.Set("X-Muvee-Internal-Key", c.key)
			}
			w := httptest.NewRecorder()
			s.handleInternalAuthIdentityUpsert(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, c.wantStatus)
			}
		})
	}
}

func TestHandleInternalAuthIdentityUpsert_RejectsBadPayload(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	key := internalAPIKey()
	s := &Server{}

	// Note: identity-upsert does NOT require a `provider` field (unlike upsert)
	// because there is no domain check or invite gate to apply.
	cases := []struct {
		name, body string
		wantStatus int
	}{
		{"invalid json", `{not json`, http.StatusBadRequest},
		{"missing email", `{}`, http.StatusBadRequest},
		{"whitespace email", `{"email":"   "}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/identity-upsert",
				strings.NewReader(c.body))
			r.Header.Set("X-Muvee-Internal-Key", key)
			w := httptest.NewRecorder()
			s.handleInternalAuthIdentityUpsert(w, r)
			if w.Code != c.wantStatus {
				t.Errorf("got status %d, want %d (body=%s)", w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}
