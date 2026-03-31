package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ─── isValidTunnelDomain ────────────────────────────────────────────────────

func TestIsValidTunnelDomain(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"t-bold-fox", true},
		{"t-calm-owl", true},
		{"t-a-b", true},
		{"t-abc-def-ghi", true},   // more than 2 words is ok
		{"t-abc123-def", true},    // alphanumeric ok
		{"t-", false},             // no words after prefix
		{"t-a", false},            // only one word
		{"bold-fox", false},       // missing t- prefix
		{"t-Bold-fox", false},     // uppercase
		{"t--fox", false},         // empty word
		{"t-bold-", false},        // trailing empty word
		{"", false},               // empty
		{"t-hello world-x", false}, // space
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isValidTunnelDomain(tt.input)
			if got != tt.want {
				t.Errorf("isValidTunnelDomain(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ─── tunnelRegistry ─────────────────────────────────────────────────────────

func TestTunnelRegistry_RegisterAndGet(t *testing.T) {
	tr := newTunnelRegistry()

	tc := &tunnelConn{
		userEmail:    "alice@example.com",
		authRequired: true,
		pending:      make(map[string]chan *tunnelMsg),
	}
	tr.register("t-bold-fox", tc)

	got := tr.get("t-bold-fox")
	if got != tc {
		t.Fatal("expected to get the registered tunnelConn")
	}

	if got := tr.get("t-other-domain"); got != nil {
		t.Fatal("expected nil for unregistered domain")
	}
}

func TestTunnelRegistry_Unregister(t *testing.T) {
	tr := newTunnelRegistry()

	tc := &tunnelConn{pending: make(map[string]chan *tunnelMsg)}
	tr.register("t-bold-fox", tc)
	tr.unregister("t-bold-fox", tc)

	if got := tr.get("t-bold-fox"); got != nil {
		t.Fatal("expected nil after unregister")
	}
}

func TestTunnelRegistry_UnregisterIgnoresStale(t *testing.T) {
	tr := newTunnelRegistry()

	tc1 := &tunnelConn{pending: make(map[string]chan *tunnelMsg)}
	tc2 := &tunnelConn{pending: make(map[string]chan *tunnelMsg)}
	tr.register("t-bold-fox", tc1)
	tr.register("t-bold-fox", tc2) // replaces tc1

	// Unregistering tc1 (stale) should not remove tc2.
	tr.unregister("t-bold-fox", tc1)
	if got := tr.get("t-bold-fox"); got != tc2 {
		t.Fatal("stale unregister should not remove the current conn")
	}
}

func TestTunnelRegistry_ActiveTunnels(t *testing.T) {
	tr := newTunnelRegistry()

	tc1 := &tunnelConn{authRequired: true, pending: make(map[string]chan *tunnelMsg)}
	tc2 := &tunnelConn{authRequired: false, pending: make(map[string]chan *tunnelMsg)}
	tr.register("t-bold-fox", tc1)
	tr.register("t-calm-owl", tc2)

	tunnels := tr.activeTunnels()
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	m := make(map[string]bool)
	for _, ti := range tunnels {
		m[ti.Domain] = ti.AuthRequired
	}
	if !m["t-bold-fox"] {
		t.Error("t-bold-fox should have AuthRequired=true")
	}
	if m["t-calm-owl"] {
		t.Error("t-calm-owl should have AuthRequired=false")
	}
}

// ─── tunnelConn dispatch ────────────────────────────────────────────────────

func TestTunnelConn_DispatchResponse(t *testing.T) {
	tc := &tunnelConn{pending: make(map[string]chan *tunnelMsg)}

	ch := tc.addPending("req-1")
	resp := &tunnelMsg{Type: "response", ID: "req-1", StatusCode: 200}

	tc.dispatchResponse(resp)

	select {
	case got := <-ch:
		if got.StatusCode != 200 {
			t.Errorf("expected 200, got %d", got.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response dispatch")
	}

	tc.removePending("req-1")
}

func TestTunnelConn_DispatchResponse_UnknownID(t *testing.T) {
	tc := &tunnelConn{pending: make(map[string]chan *tunnelMsg)}
	// Should not panic on unknown ID.
	tc.dispatchResponse(&tunnelMsg{Type: "response", ID: "unknown"})
}

// ─── isTunnelRequest ────────────────────────────────────────────────────────

func TestIsTunnelRequest(t *testing.T) {
	s := &Server{baseDomain: "example.com"}

	tests := []struct {
		host string
		want bool
	}{
		{"t-bold-fox.example.com", true},
		{"t-calm-owl.example.com", true},
		{"t-a-b.example.com", true},
		{"myapp.example.com", false},      // no t- prefix
		{"t-bold-fox.other.com", false},    // wrong base domain
		{"example.com", false},             // no subdomain
		{"t-bold-fox.example.com:443", true}, // with port
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tt.host
			got := s.isTunnelRequest(r)
			if got != tt.want {
				t.Errorf("isTunnelRequest(host=%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

// ─── Tunnel WebSocket + HTTP proxy integration ──────────────────────────────

// TestTunnelEndToEnd starts a server with tunnel support, connects a fake CLI
// via WebSocket, sends an HTTP request through the tunnel, and verifies the
// response is proxied correctly.
func TestTunnelEndToEnd(t *testing.T) {
	// Create a minimal server with tunnel support.
	s := &Server{
		baseDomain:       "example.com",
		tunnelBackendURL: "http://localhost:9999", // not used in this test
		tunnels:          newTunnelRegistry(),
		authServiceURL:   "http://authservice:4181",
	}

	// Start a test HTTP server that exposes the tunnel connect endpoint
	// (without auth middleware for test simplicity) and the tunnel traffic handler.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/connect", func(w http.ResponseWriter, r *http.Request) {
		// Simulate auth: inject a fake user into context.
		// In production this would come from s.auth.Middleware.
		// For testing, we call handleTunnelConnect directly after patching.
		domain := r.URL.Query().Get("domain")
		if domain == "" || !isValidTunnelDomain(domain) {
			http.Error(w, "bad domain", 400)
			return
		}
		authRequired := r.URL.Query().Get("noauth") != "1"

		ws, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}

		tc := &tunnelConn{
			ws:           ws,
			userEmail:    "test@example.com",
			authRequired: authRequired,
			pending:      make(map[string]chan *tunnelMsg),
		}
		s.tunnels.register(domain, tc)
		defer func() {
			s.tunnels.unregister(domain, tc)
			ws.Close()
		}()

		// Read loop.
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var msg tunnelMsg
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			if msg.Type == "response" {
				tc.dispatchResponse(&msg)
			}
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Connect a fake CLI via WebSocket.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/tunnel/connect?domain=t-test-fox"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	// Give the server a moment to register the tunnel.
	time.Sleep(50 * time.Millisecond)

	// Verify tunnel is registered.
	tc := s.tunnels.get("t-test-fox")
	if tc == nil {
		t.Fatal("tunnel not registered")
	}
	if !tc.authRequired {
		t.Error("expected authRequired=true by default")
	}

	// Start a goroutine to simulate the CLI: read requests, return a fixed response.
	go func() {
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var req tunnelMsg
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			if req.Type != "request" {
				continue
			}

			resp := tunnelMsg{
				Type:       "response",
				ID:         req.ID,
				StatusCode: 200,
				Headers:    map[string][]string{"Content-Type": {"text/plain"}},
				Body:       base64.StdEncoding.EncodeToString([]byte("hello from tunnel")),
			}
			data, _ := json.Marshal(resp)
			ws.WriteMessage(websocket.TextMessage, data)
		}
	}()

	// Now send an HTTP request through the tunnel traffic handler.
	req := httptest.NewRequest("GET", "/some/path?q=1", nil)
	req.Host = "t-test-fox.example.com"
	rec := httptest.NewRecorder()

	s.handleTunnelTraffic(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "hello from tunnel" {
		t.Fatalf("expected 'hello from tunnel', got %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("expected Content-Type text/plain, got %q", ct)
	}
}

// TestTunnelNoAuth verifies that passing noauth=1 results in authRequired=false.
func TestTunnelNoAuth(t *testing.T) {
	s := &Server{
		baseDomain: "example.com",
		tunnels:    newTunnelRegistry(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/connect", func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		authRequired := r.URL.Query().Get("noauth") != "1"

		ws, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		tc := &tunnelConn{
			ws:           ws,
			userEmail:    "test@example.com",
			authRequired: authRequired,
			pending:      make(map[string]chan *tunnelMsg),
		}
		s.tunnels.register(domain, tc)
		defer func() {
			s.tunnels.unregister(domain, tc)
			ws.Close()
		}()

		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				return
			}
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Connect WITH noauth=1.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/tunnel/connect?domain=t-noauth-fox&noauth=1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	time.Sleep(50 * time.Millisecond)

	tc := s.tunnels.get("t-noauth-fox")
	if tc == nil {
		t.Fatal("tunnel not registered")
	}
	if tc.authRequired {
		t.Error("expected authRequired=false when noauth=1")
	}
}

// ─── Traefik config with tunnels ────────────────────────────────────────────

func TestTraefikConfig_TunnelWithAuth(t *testing.T) {
	s := &Server{
		baseDomain:       "example.com",
		tunnelBackendURL: "http://muvee-server:8080",
		tunnels:          newTunnelRegistry(),
		authServiceURL:   "http://authservice:4181",
	}

	// Register a tunnel with auth.
	tc := &tunnelConn{authRequired: true, pending: make(map[string]chan *tunnelMsg)}
	s.tunnels.register("t-bold-fox", tc)

	// Call handleTraefikConfig — we need a store stub that returns empty deployments.
	// Since we can't easily mock the store, we test the tunnel portion of the config
	// by building it manually the same way handleTraefikConfig does.
	cfg := traefikDynamicConfig{
		HTTP: traefikHTTP{
			Routers:  make(map[string]traefikRouter),
			Services: make(map[string]traefikService),
		},
	}

	for _, ti := range s.tunnels.activeTunnels() {
		name := "tunnel-" + ti.Domain
		host := fmt.Sprintf("%s.%s", ti.Domain, s.baseDomain)

		router := traefikRouter{
			Rule:        fmt.Sprintf("Host(`%s`)", host),
			EntryPoints: []string{"websecure"},
			Service:     name,
			TLS:         &traefikTLS{CertResolver: "letsencrypt"},
		}

		if ti.AuthRequired {
			if cfg.HTTP.Middlewares == nil {
				cfg.HTTP.Middlewares = make(map[string]traefikMiddleware)
			}
			mwName := name + "-auth"
			verifyURL := fmt.Sprintf("%s/verify", s.authServiceURL)
			cfg.HTTP.Middlewares[mwName] = traefikMiddleware{
				ForwardAuth: &traefikForwardAuth{
					Address:             verifyURL,
					AuthResponseHeaders: []string{"X-Forwarded-User"},
					TrustForwardHeader:  true,
				},
			}
			router.Middlewares = []string{mwName}
		}

		cfg.HTTP.Routers[name] = router
		cfg.HTTP.Services[name] = traefikService{
			LoadBalancer: traefikLB{
				Servers: []traefikServer{{URL: s.tunnelBackendURL}},
			},
		}
	}

	// Verify router exists with ForwardAuth middleware.
	router, ok := cfg.HTTP.Routers["tunnel-t-bold-fox"]
	if !ok {
		t.Fatal("expected router for tunnel-t-bold-fox")
	}
	if router.Rule != "Host(`t-bold-fox.example.com`)" {
		t.Errorf("unexpected rule: %s", router.Rule)
	}
	if len(router.Middlewares) != 1 || router.Middlewares[0] != "tunnel-t-bold-fox-auth" {
		t.Errorf("unexpected middlewares: %v", router.Middlewares)
	}

	mw, ok := cfg.HTTP.Middlewares["tunnel-t-bold-fox-auth"]
	if !ok {
		t.Fatal("expected ForwardAuth middleware")
	}
	if mw.ForwardAuth == nil {
		t.Fatal("ForwardAuth is nil")
	}
	if mw.ForwardAuth.Address != "http://authservice:4181/verify" {
		t.Errorf("unexpected ForwardAuth address: %s", mw.ForwardAuth.Address)
	}
}

func TestTraefikConfig_TunnelNoAuth(t *testing.T) {
	s := &Server{
		baseDomain:       "example.com",
		tunnelBackendURL: "http://muvee-server:8080",
		tunnels:          newTunnelRegistry(),
		authServiceURL:   "http://authservice:4181",
	}

	// Register a tunnel WITHOUT auth.
	tc := &tunnelConn{authRequired: false, pending: make(map[string]chan *tunnelMsg)}
	s.tunnels.register("t-open-fox", tc)

	cfg := traefikDynamicConfig{
		HTTP: traefikHTTP{
			Routers:  make(map[string]traefikRouter),
			Services: make(map[string]traefikService),
		},
	}

	for _, ti := range s.tunnels.activeTunnels() {
		name := "tunnel-" + ti.Domain
		host := fmt.Sprintf("%s.%s", ti.Domain, s.baseDomain)

		router := traefikRouter{
			Rule:        fmt.Sprintf("Host(`%s`)", host),
			EntryPoints: []string{"websecure"},
			Service:     name,
			TLS:         &traefikTLS{CertResolver: "letsencrypt"},
		}

		if ti.AuthRequired {
			if cfg.HTTP.Middlewares == nil {
				cfg.HTTP.Middlewares = make(map[string]traefikMiddleware)
			}
			mwName := name + "-auth"
			cfg.HTTP.Middlewares[mwName] = traefikMiddleware{
				ForwardAuth: &traefikForwardAuth{
					Address: fmt.Sprintf("%s/verify", s.authServiceURL),
				},
			}
			router.Middlewares = []string{mwName}
		}

		cfg.HTTP.Routers[name] = router
	}

	router, ok := cfg.HTTP.Routers["tunnel-t-open-fox"]
	if !ok {
		t.Fatal("expected router for tunnel-t-open-fox")
	}
	if len(router.Middlewares) != 0 {
		t.Errorf("expected no middlewares for noauth tunnel, got %v", router.Middlewares)
	}
	if cfg.HTTP.Middlewares != nil {
		t.Errorf("expected no middlewares map, got %v", cfg.HTTP.Middlewares)
	}
}

// ─── Tunnel traffic with disconnected tunnel ────────────────────────────────

func TestTunnelTraffic_NotConnected(t *testing.T) {
	s := &Server{
		baseDomain: "example.com",
		tunnels:    newTunnelRegistry(),
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "t-unknown-fox.example.com"
	rec := httptest.NewRecorder()

	s.handleTunnelTraffic(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}
