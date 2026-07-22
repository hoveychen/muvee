package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/auth"
)

func TestPlatformSMS_DisabledReturns404(t *testing.T) {
	// PLATFORM_PHONE_LOGIN unset → feature off → both endpoints 404 before any
	// store access.
	t.Setenv("PLATFORM_PHONE_LOGIN", "")
	s := &Server{}
	for _, h := range []http.HandlerFunc{s.handlePlatformSMSSendCode, s.handlePlatformSMSVerify} {
		r := httptest.NewRequest(http.MethodPost, "/auth/phone/x", strings.NewReader(`{"phone":"13800138000"}`))
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("disabled endpoint: got %d, want 404", w.Code)
		}
	}
}

// enabled + malformed input is rejected before the handler touches the store.
func TestPlatformSMS_ValidationBeforeStore(t *testing.T) {
	t.Setenv("PLATFORM_PHONE_LOGIN", "true")
	s := &Server{}
	cases := []struct {
		name, body string
		h          http.HandlerFunc
		want       int
	}{
		{"send bad json", `{`, s.handlePlatformSMSSendCode, http.StatusBadRequest},
		{"send bad phone", `{"phone":"123"}`, s.handlePlatformSMSSendCode, http.StatusBadRequest},
		{"verify bad phone", `{"phone":"x","code":"123456"}`, s.handlePlatformSMSVerify, http.StatusBadRequest},
		{"verify empty code", `{"phone":"13800138000","code":""}`, s.handlePlatformSMSVerify, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/phone/x", strings.NewReader(c.body))
			w := httptest.NewRecorder()
			c.h(w, r)
			if w.Code != c.want {
				t.Errorf("got %d, want %d (%s)", w.Code, c.want, w.Body.String())
			}
		})
	}
}

func TestHandleListProviders_PhoneLoginFlag(t *testing.T) {
	s := &Server{auth: &auth.Service{}}
	read := func() bool {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
		w := httptest.NewRecorder()
		s.handleListProviders(w, r)
		var out struct {
			PhoneLogin bool `json:"phone_login"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
		return out.PhoneLogin
	}
	t.Setenv("PLATFORM_PHONE_LOGIN", "")
	if read() {
		t.Error("phone_login should be false when disabled")
	}
	t.Setenv("PLATFORM_PHONE_LOGIN", "true")
	if !read() {
		t.Error("phone_login should be true when enabled")
	}
}
