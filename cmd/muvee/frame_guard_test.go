package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The console must refuse to be framed (BACKLOG-20261124-001) on every page
// it serves — the SPA that mountFrontend serves without Router included — but
// tenant traffic (tunnel Hosts, the port-forward proxy) must keep the tenant's
// own headers.
func TestConsoleHandler_AntiFraming(t *testing.T) {
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	const tunnelHost = "t-bold-fox.example.com"
	tunnelAware := func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host == tunnelHost {
				w.WriteHeader(http.StatusOK) // tenant response
				return
			}
			inner.ServeHTTP(w, r)
		})
	}
	h := consoleHandler(tunnelAware, router)

	tests := []struct {
		name, host, path string
		framed           bool // true = headers must be absent
	}{
		{"spa root", "example.com", "/", false},
		{"spa deep link", "example.com", "/projects/abc/settings", false},
		{"spa asset", "example.com", "/index.html", false},
		{"api", "example.com", "/api/projects", false},
		{"auth", "example.com", "/auth/google/callback", false},
		{"git", "example.com", "/git/repo.git/info/refs", false},
		{"proxy look-alike", "example.com", "/api/projects/abc/proxyx", false},
		{"proxy without id", "example.com", "/api/projects//proxy", false},
		{"port-forward proxy", "example.com", "/api/projects/abc/proxy", true},
		{"port-forward proxy subpath", "example.com", "/api/projects/abc/proxy/x/y?q=1", true},
		{"tunnel host", tunnelHost, "/", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			r.Host = tt.host
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			csp, xfo := w.Header().Get("Content-Security-Policy"), w.Header().Get("X-Frame-Options")
			if tt.framed {
				if csp != "" || xfo != "" {
					t.Errorf("tenant response got CSP=%q XFO=%q, want none", csp, xfo)
				}
				return
			}
			if csp != "frame-ancestors 'none'" || xfo != "DENY" {
				t.Errorf("console response CSP=%q XFO=%q, want frame-ancestors 'none' / DENY", csp, xfo)
			}
		})
	}
}
