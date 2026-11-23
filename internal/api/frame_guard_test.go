package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// With the real TunnelAwareHandler outside it, AntiFramingHandler must never
// touch tunnel / domain_only tenant responses (BACKLOG-20261124-001).
func TestAntiFraming_SkipsTunnelHosts(t *testing.T) {
	s := &Server{
		baseDomain:         "example.com",
		baseDomains:        []string{"example.com"},
		domainOnlyPrefixes: map[string]bool{"reserved": true},
		tunnels:            newTunnelRegistry(),
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := s.TunnelAwareHandler(AntiFramingHandler(inner))

	for _, tt := range []struct {
		host   string
		tunnel bool
	}{
		{"reserved.example.com", true}, // domain_only, offline placeholder
		{"t-bold-fox.example.com", true},
		{"example.com", false},
		{"app.example.com", false}, // ordinary subsite host reaching muvee-server
	} {
		t.Run(tt.host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tt.host
			if got := s.isTunnelRequest(r); got != tt.tunnel {
				t.Fatalf("isTunnelRequest(%q) = %v, want %v", tt.host, got, tt.tunnel)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			has := w.Header().Get("X-Frame-Options") != "" || w.Header().Get("Content-Security-Policy") != ""
			if has == tt.tunnel {
				t.Errorf("host %q: anti-framing headers present=%v, want %v", tt.host, has, !tt.tunnel)
			}
		})
	}
}

// A proxied tenant response keeps its own framing policy: the guard must not
// pre-set headers that ReverseProxy would then merge with the upstream's.
func TestAntiFraming_ProxyKeepsUpstreamHeaders(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Frame-Options", "SAMEORIGIN")
	})
	r := httptest.NewRequest("GET", "/api/projects/abc/proxy/", nil)
	w := httptest.NewRecorder()
	AntiFramingHandler(upstream).ServeHTTP(w, r)
	if got := w.Header().Values("X-Frame-Options"); len(got) != 1 || got[0] != "SAMEORIGIN" {
		t.Errorf("proxied XFO = %q, want only the upstream's SAMEORIGIN", got)
	}
	if csp := w.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("proxied response got CSP %q", csp)
	}
}
