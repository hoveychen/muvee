package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The console API trusts muvee_session (SameSite=Lax), and tenant subsites are
// same-site with the console, so a tenant page could fire a cross-origin
// simple request that the browser sends with the cookie. crossOriginGuard
// refuses every unsafe-method request that a browser marks as coming from
// another origin (BUG-20261123-001):
//
//   - Sec-Fetch-Site present and not same-origin → 403
//   - no Sec-Fetch-Site (old browsers, plain-HTTP deployments) but an Origin
//     that is not the request's own host → 403
//   - requests authenticated with "Bearer " (muveectl, PATs) are exempt: a
//     cross-origin page cannot attach Authorization without passing CORS
//   - safe methods and header-less requests (curl, server-to-server) pass
func TestCrossOriginGuard(t *testing.T) {
	s := &Server{}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.crossOriginGuard(ok)

	cases := []struct {
		name   string
		method string
		sfs    string
		origin string
		authz  string
		want   int
	}{
		{"POST same-origin", http.MethodPost, "same-origin", "", "", 200},
		{"POST no headers (curl, server-to-server)", http.MethodPost, "", "", "", 200},
		{"POST same-site", http.MethodPost, "same-site", "", "", 403},
		{"POST cross-site", http.MethodPost, "cross-site", "", "", 403},
		{"POST none", http.MethodPost, "none", "", "", 403},
		{"PUT cross-site", http.MethodPut, "cross-site", "", "", 403},
		{"PATCH same-site", http.MethodPatch, "same-site", "", "", 403},
		{"DELETE cross-site", http.MethodDelete, "cross-site", "", "", 403},
		{"GET cross-site", http.MethodGet, "cross-site", "", "", 200},
		{"HEAD cross-site", http.MethodHead, "cross-site", "", "", 200},
		{"OPTIONS cross-site", http.MethodOptions, "cross-site", "", "", 200},
		{"POST cross-site with Bearer", http.MethodPost, "cross-site", "", "Bearer mvp_x", 200},
		{"POST same-site with Bearer (port-forward relays browser headers)", http.MethodPost, "same-site", "https://evil.site-a.test", "Bearer mvp_x", 200},
		// extractToken only honours the exact, case-sensitive "Bearer " prefix;
		// anything else falls back to the cookie, so it must not be exempt.
		{"POST cross-site with lowercase bearer", http.MethodPost, "cross-site", "", "bearer mvp_x", 403},
		{"POST cross-site with Basic", http.MethodPost, "cross-site", "", "Basic dTpw", 403},
		{"POST no SFS, foreign Origin", http.MethodPost, "", "https://evil.site-a.test", "", 403},
		{"POST no SFS, cross-site Origin", http.MethodPost, "", "https://site-b.test", "", 403},
		{"POST no SFS, Origin null", http.MethodPost, "", "null", "", 403},
		{"POST no SFS, own Origin", http.MethodPost, "", "https://site-a.test", "", 200},
		{"POST no SFS, own Origin different case", http.MethodPost, "", "https://Site-A.test", "", 200},
		{"POST no SFS, foreign Origin with Bearer", http.MethodPost, "", "https://evil.site-a.test", "Bearer mvp_x", 200},
		{"POST same-origin wins over Origin", http.MethodPost, "same-origin", "https://site-a.test", "", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, "https://site-a.test/api/me/tokens", nil)
			if c.sfs != "" {
				r.Header.Set("Sec-Fetch-Site", c.sfs)
			}
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if c.authz != "" {
				r.Header.Set("Authorization", c.authz)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Errorf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}

// The origin comparison must use the host the browser addressed, including a
// non-default port, so a page on another port of the same host is foreign.
func TestCrossOriginGuard_OriginPort(t *testing.T) {
	h := (&Server{}).crossOriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, c := range []struct {
		host, origin string
		want         int
	}{
		{"site-a.test:8443", "https://site-a.test:8443", 200},
		{"site-a.test:8443", "https://site-a.test:9443", 403},
		{"site-a.test:8443", "https://site-a.test", 403},
	} {
		r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		r.Host = c.host
		r.Header.Set("Origin", c.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("host %s origin %s: status = %d, want %d", c.host, c.origin, w.Code, c.want)
		}
	}
}

// The guard has to be wired into Router() itself — covering /auth/logout,
// /auth/phone/*, and every /api route — and not only exist as a helper.
func TestRouter_RefusesCrossOriginWrites(t *testing.T) {
	router := (&Server{}).Router()
	for _, path := range []string{
		"/auth/logout",
		"/auth/phone/verify",
		"/api/me/tokens",
		"/api/me/cli-tokens/revoke",
		"/api/me/fwd-sessions/revoke",
		"/api/projects/00000000-0000-0000-0000-000000000001/deploy",
		"/api/projects/00000000-0000-0000-0000-000000000001/proxy/x",
	} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "https://site-a.test"+path, nil)
			r.Header.Set("Sec-Fetch-Site", "same-site")
			r.AddCookie(&http.Cookie{Name: "muvee_session", Value: "x"})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
		})
	}
}
