package api

import (
	"net/http"
	"net/url"
	"strings"
)

// hasBearer reports whether the request authenticates with the exact,
// case-sensitive "Bearer " prefix — the same test auth.extractToken uses to
// pick the Authorization header over the muvee_session cookie. Any other
// Authorization value (lowercase "bearer", Basic) falls back to the cookie, so
// it must not earn the exemptions granted to Bearer callers.
func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// crossOriginGuard refuses unsafe-method requests that a browser sent from
// another origin (BUG-20261123-001).
//
// muvee_session is SameSite=Lax and tenant subsites are same-site with the
// console, so a tenant page can fire a simple cross-origin POST (no body, or
// text/plain) that rides on the visitor's console session. The rule, shared
// with authservice's /_oauth/logout:
//
//   - GET / HEAD / OPTIONS pass.
//   - "Bearer " requests pass: a cross-origin page cannot attach Authorization
//     without passing CORS, and muveectl port-forward relays the browser's own
//     Sec-Fetch-Site while replacing credentials with a Bearer token.
//   - Sec-Fetch-Site present: only same-origin passes.
//   - Sec-Fetch-Site absent (browsers without Fetch Metadata, plain-HTTP
//     deployments): an Origin header that is not the request's own host is
//     refused; no Origin at all (curl, server-to-server) passes.
//
// It is mounted inside Router(), so tunnel / domain-only tenant traffic that
// TunnelAwareHandler routes away before Router() never sees it.
func (s *Server) crossOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuseCrossOriginWrite(r) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func refuseCrossOriginWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if hasBearer(r) {
		return false
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs != "same-origin"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	return !originMatchesHost(origin, r.Host)
}

// originMatchesHost compares an Origin header against the Host the browser
// addressed (host and port, case-insensitive). The scheme is not compared:
// behind TLS-terminating Traefik the server cannot see it reliably, and a
// browser that sends Fetch Metadata is already judged schemefully above.
// "null" and unparsable origins never match.
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
