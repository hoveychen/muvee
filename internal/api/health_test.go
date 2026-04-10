package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckHTTP_RedirectLoop(t *testing.T) {
	// Simulate a server that always redirects (like Traefik with ForwardAuth
	// redirecting to OAuth login when no session cookie is present).
	redirectCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCount++
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()

	result := checkHTTP("traefik", srv.URL, 5e9)

	if result.Status != HealthCheckOK {
		t.Errorf("expected status OK for a responding-but-redirecting server, got %s: %s", result.Status, result.Message)
	}
}
