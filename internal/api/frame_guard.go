package api

import (
	"net/http"
	"strings"
)

// consoleFrameAncestors is the CSP sent with every console response.
const consoleFrameAncestors = "frame-ancestors 'none'"

// AntiFramingHandler forbids other pages from framing the console
// (BACKLOG-20261124-001, clickjacking). Tenant subsites are same-site with the
// console, so without this a tenant page could load the console in a
// transparent iframe and trick an admin into clicking a destructive action.
//
// Placement matters. cmd/muvee mounts it outside mountFrontend, so the
// console SPA pages (served by webui, not Router) are covered along with /api,
// /auth and /git/; and inside TunnelAwareHandler, so tunnel / domain_only
// tenant traffic is routed away first and keeps the tenant's own headers. It
// is deliberately not set in traefik: the console host's /_oauth/* is
// authservice's, and a subsite signing in inside an iframe redirects through
// it.
//
// /api/projects/{id}/proxy[/...] is skipped: it is muveectl port-forward's
// backend and returns the tenant's own pages, which may legitimately be
// framed. httputil.ReverseProxy adds upstream headers to ours rather than
// replacing them, so the skip has to happen here.
func AntiFramingHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isProjectProxyPath(r.URL.Path) {
			h := w.Header()
			h.Set("Content-Security-Policy", consoleFrameAncestors)
			h.Set("X-Frame-Options", "DENY")
		}
		next.ServeHTTP(w, r)
	})
}

// isProjectProxyPath reports whether p is /api/projects/{id}/proxy or a path
// under it, with {id} a single non-empty segment — the shape Router() sends
// to handleProjectProxy. Anything else (including look-alikes) keeps the
// headers, so a mismatch can only ever err towards protecting.
func isProjectProxyPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/api/projects/")
	if !ok {
		return false
	}
	id, tail, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		return false
	}
	return tail == "proxy" || strings.HasPrefix(tail, "proxy/")
}
