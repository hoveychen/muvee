package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestSMSHandlers_RejectMissingOrWrongKey(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	s := &Server{}
	handlers := map[string]http.HandlerFunc{
		"send-code": s.handleInternalAuthSMSSendCode,
		"verify":    s.handleInternalAuthSMSVerify,
	}
	for name, h := range handlers {
		for _, key := range []string{"", "deadbeef"} {
			r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/sms/"+name,
				strings.NewReader(`{"project_id":"x","phone":"13800138000"}`))
			if key != "" {
				r.Header.Set("X-Muvee-Internal-Key", key)
			}
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s key=%q: got %d, want 401", name, key, w.Code)
			}
		}
	}
}

// validation errors are reported before the handler ever touches the store, so
// these run against a store-less Server.
func TestSMSHandlers_ValidationBeforeStore(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	s := &Server{}
	key := internalAPIKey()

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"send bad json", "send-code", `{`, http.StatusBadRequest},
		{"send bad project", "send-code", `{"project_id":"nope","phone":"13800138000"}`, http.StatusBadRequest},
		{"send bad phone", "send-code", `{"project_id":"11111111-1111-1111-1111-111111111111","phone":"123"}`, http.StatusBadRequest},
		{"verify bad project", "verify", `{"project_id":"nope","phone":"13800138000","code":"123456"}`, http.StatusBadRequest},
		{"verify bad phone", "verify", `{"project_id":"11111111-1111-1111-1111-111111111111","phone":"x","code":"123456"}`, http.StatusBadRequest},
		{"verify empty code", "verify", `{"project_id":"11111111-1111-1111-1111-111111111111","phone":"13800138000","code":""}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := s.handleInternalAuthSMSSendCode
			if c.path == "verify" {
				h = s.handleInternalAuthSMSVerify
			}
			r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/sms/"+c.path, strings.NewReader(c.body))
			r.Header.Set("X-Muvee-Internal-Key", key)
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != c.want {
				t.Errorf("got %d, want %d (body=%s)", w.Code, c.want, w.Body.String())
			}
		})
	}
}

func TestGenerateSMSCode(t *testing.T) {
	re := regexp.MustCompile(`^\d{6}$`)
	for i := 0; i < 50; i++ {
		code, err := generateSMSCode()
		if err != nil {
			t.Fatalf("generateSMSCode error: %v", err)
		}
		if !re.MatchString(code) {
			t.Fatalf("code %q is not 6 digits", code)
		}
	}
}

func TestHashSMSCode(t *testing.T) {
	if hashSMSCode("123456") != hashSMSCode("123456") {
		t.Fatal("hash not deterministic")
	}
	if hashSMSCode("123456") == hashSMSCode("654321") {
		t.Fatal("distinct codes produced same hash")
	}
}
