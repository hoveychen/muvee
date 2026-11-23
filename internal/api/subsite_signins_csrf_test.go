package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// POST /api/me/fwd-sessions/revoke takes no body, so a tenant page on a
// sibling subsite (same site as the console, muvee_session is SameSite=Lax)
// can fire it as a simple cross-origin request without a preflight and sign
// the visitor out of every subsite. Browsers mark such requests with
// Sec-Fetch-Site != same-origin: reject them before touching the store.
// Requests without the header (curl, muveectl) keep working, matching
// authservice's handleFwdLogout confirm-page rule.
func TestRevokeFwdSessions_RejectsCrossOrigin(t *testing.T) {
	s := &Server{} // nil store: reaching it would panic
	u := &store.User{Email: "csrf@example.com"}
	for _, sfs := range []string{"same-site", "cross-site", "none"} {
		t.Run("Sec-Fetch-Site="+sfs, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/me/fwd-sessions/revoke", nil)
			r.Header.Set("Sec-Fetch-Site", sfs)
			r = r.WithContext(context.WithValue(r.Context(), auth.CtxUserKey, u))
			w := httptest.NewRecorder()
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Errorf("request reached the store (panic: %v); want 403 first", rec)
					}
				}()
				s.handleRevokeFwdSessions(w, r)
			}()
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
	// same-origin and header-less requests pass the check (and then hit the
	// normal auth path: no user → 401, proving they were not 403'd).
	for _, sfs := range []string{"same-origin", ""} {
		t.Run("pass/Sec-Fetch-Site="+sfs, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/me/fwd-sessions/revoke", nil)
			if sfs != "" {
				r.Header.Set("Sec-Fetch-Site", sfs)
			}
			w := httptest.NewRecorder()
			s.handleRevokeFwdSessions(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (check must let it through)", w.Code)
			}
		})
	}
}
