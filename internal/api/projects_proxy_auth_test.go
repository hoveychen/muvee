package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// The project proxy serves tenant container responses on the console origin.
// If a browser session cookie could authenticate it, tenant JavaScript would
// run same-origin with the console and could mint tokens for whoever opened
// it (an admin lured via a same-site iframe or a redirect). The proxy was
// built for muveectl curl / port-forward, which always send "Bearer ", so it
// must refuse anything else before touching the store (BUG-20261123-001).
func TestProjectProxy_RequiresBearer(t *testing.T) {
	s := &Server{} // nil store: reaching it panics
	u := &store.User{Email: "admin@example.com", Role: store.UserRoleAdmin}
	const id = "00000000-0000-0000-0000-000000000001"

	newReq := func(method, authz string) *http.Request {
		r := httptest.NewRequest(method, "/api/projects/"+id+"/proxy/", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		ctx = context.WithValue(ctx, auth.CtxUserKey, u)
		r = r.WithContext(ctx)
		r.AddCookie(&http.Cookie{Name: "muvee_session", Value: "jwt"})
		if authz != "" {
			r.Header.Set("Authorization", authz)
		}
		return r
	}

	for _, c := range []struct{ method, authz string }{
		{http.MethodGet, ""},
		{http.MethodPost, ""},
		{http.MethodGet, "bearer mvp_x"},
		{http.MethodGet, "Basic dTpw"},
	} {
		t.Run(c.method+" "+c.authz, func(t *testing.T) {
			w := httptest.NewRecorder()
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Errorf("cookie-authenticated request reached the store (panic: %v); want 401 first", rec)
					}
				}()
				s.handleProjectProxy(w, newReq(c.method, c.authz))
			}()
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}

	t.Run("Bearer passes the check", func(t *testing.T) {
		reachedStore := false
		func() {
			defer func() {
				if recover() != nil {
					reachedStore = true
				}
			}()
			s.handleProjectProxy(httptest.NewRecorder(), newReq(http.MethodGet, "Bearer mvp_x"))
		}()
		if !reachedStore {
			t.Error("Bearer request did not proceed to the access check")
		}
	})
}
