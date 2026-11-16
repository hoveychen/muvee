package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Under BASE_DOMAINS a tunnel / domain_only prefix must be recognised on every
// served apex, not only the canonical one. Before the fix, a request for
// `reserved.alias.com` fell through isTunnelRequest and was served the
// platform console SPA instead of the tunnel.
func TestIsTunnelRequest_MultiDomain(t *testing.T) {
	s := &Server{
		baseDomain:         "example.com",
		baseDomains:        []string{"example.com", "alias.com"},
		domainOnlyPrefixes: map[string]bool{"reserved": true},
	}

	tests := []struct {
		host string
		want bool
	}{
		{"reserved.example.com", true},
		{"reserved.alias.com", true},
		{"reserved.alias.com:443", true},
		{"t-bold-fox.alias.com", true},
		{"unreserved.alias.com", false},
		{"alias.com", false}, // apex itself is the console, never a tunnel
		{"reserved.other.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tt.host
			if got := s.isTunnelRequest(r); got != tt.want {
				t.Errorf("isTunnelRequest(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

// handleTunnelTraffic must derive the same tunnel prefix on an alias apex: a
// domain_only prefix with no live tunnel gets the offline placeholder (503),
// not "unknown tunnel host" (502).
func TestTunnelTraffic_MultiDomainOfflinePlaceholder(t *testing.T) {
	s := &Server{
		baseDomain:         "example.com",
		baseDomains:        []string{"example.com", "alias.com"},
		tunnels:            newTunnelRegistry(),
		domainOnlyPrefixes: map[string]bool{"reserved": true},
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "reserved.alias.com"
	rec := httptest.NewRecorder()

	s.handleTunnelTraffic(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 offline placeholder, got %d", rec.Code)
	}
}
