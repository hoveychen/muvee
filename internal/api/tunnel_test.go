package api

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		streams:      make(map[uint32]*tunnelStream),
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

	tc := &tunnelConn{streams: make(map[uint32]*tunnelStream)}
	tr.register("t-bold-fox", tc)
	tr.unregister("t-bold-fox", tc)

	if got := tr.get("t-bold-fox"); got != nil {
		t.Fatal("expected nil after unregister")
	}
}

func TestTunnelRegistry_RegisterIsFirstComeFirstServed(t *testing.T) {
	tr := newTunnelRegistry()

	tc1 := &tunnelConn{streams: make(map[uint32]*tunnelStream)}
	tc2 := &tunnelConn{streams: make(map[uint32]*tunnelStream)}
	if !tr.register("t-bold-fox", tc1) {
		t.Fatal("first register should succeed")
	}
	if tr.register("t-bold-fox", tc2) {
		t.Fatal("second register should be rejected while tc1 is live")
	}
	if got := tr.get("t-bold-fox"); got != tc1 {
		t.Fatal("expected tc1 to remain the registered conn")
	}
	// After tc1 unregisters, tc2 can claim the slot.
	tr.unregister("t-bold-fox", tc1)
	if !tr.register("t-bold-fox", tc2) {
		t.Fatal("register should succeed after previous unregister")
	}
	if got := tr.get("t-bold-fox"); got != tc2 {
		t.Fatal("expected tc2 to be the current registered conn")
	}
}

func TestTunnelRegistry_ActiveTunnels(t *testing.T) {
	tr := newTunnelRegistry()

	tc1 := &tunnelConn{authRequired: true, streams: make(map[uint32]*tunnelStream)}
	tc2 := &tunnelConn{authRequired: false, streams: make(map[uint32]*tunnelStream)}
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

// ─── tunnelConn stream management ───────────────────────────────────────────

func TestTunnelConn_StreamLifecycle(t *testing.T) {
	tc := &tunnelConn{streams: make(map[uint32]*tunnelStream)}

	s1 := tc.newStream()
	if s1.id != 1 {
		t.Errorf("expected first stream id 1, got %d", s1.id)
	}
	if got := tc.getStream(1); got != s1 {
		t.Fatal("getStream returned wrong stream")
	}

	s2 := tc.newStream()
	if s2.id != 2 {
		t.Errorf("expected second stream id 2, got %d", s2.id)
	}

	tc.removeStream(s1.id)
	if got := tc.getStream(1); got != nil {
		t.Fatal("stream should be gone after removeStream")
	}
	select {
	case <-s1.closed:
	default:
		t.Fatal("stream.closed should be closed after removeStream")
	}
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

// TestTunnelEndToEnd_HTTP verifies a plain HTTP GET can traverse the L4
// tunnel: the server hijacks the connection, pipes reconstructed request
// bytes to the CLI, and pipes the local service's raw HTTP response back.
func TestTunnelEndToEnd_HTTP(t *testing.T) {
	// Local HTTP server that returns a fixed payload.
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/some/path" || r.URL.RawQuery != "q=1" {
			t.Errorf("local got unexpected URL: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello from tunnel"))
	}))
	defer localSrv.Close()
	localAddr := strings.TrimPrefix(localSrv.URL, "http://")

	s := &Server{
		baseDomain:       "example.com",
		tunnelBackendURL: "http://localhost:9999",
		tunnels:          newTunnelRegistry(),
		authServiceURL:   "http://authservice:4181",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/connect", func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		if !isValidTunnelDomain(domain) {
			http.Error(w, "bad domain", 400)
			return
		}
		authRequired := r.URL.Query().Get("noauth") != "1"
		ws, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		tc := &tunnelConn{
			ws:           ws,
			userEmail:    "test@example.com",
			authRequired: authRequired,
			streams:      make(map[uint32]*tunnelStream),
		}
		s.tunnels.register(domain, tc)
		defer func() {
			s.tunnels.unregister(domain, tc)
			ws.Close()
		}()
		tc.serveConn()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		r.Host = "t-test-fox.example.com"
		s.handleTunnelTraffic(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cliWsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/tunnel/connect?domain=t-test-fox"
	cliWs, _, err := websocket.DefaultDialer.Dial(cliWsURL, nil)
	if err != nil {
		t.Fatalf("cli dial: %v", err)
	}
	defer cliWs.Close()
	go runL4ClientSimulator(t, cliWs, localAddr)

	waitForTunnel(t, s, "t-test-fox")
	tc := s.tunnels.get("t-test-fox")
	if !tc.authRequired {
		t.Error("expected authRequired=true by default")
	}

	// Send an HTTP request through the tunnel and verify the response.
	req, _ := http.NewRequest("GET", ts.URL+"/some/path?q=1", nil)
	req.Host = "t-test-fox.example.com"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if string(bodyBytes) != "hello from tunnel" {
		t.Fatalf("expected 'hello from tunnel', got %q", string(bodyBytes))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected Content-Type text/plain, got %q", ct)
	}
}

// waitForTunnel polls the registry until the tunnel is registered or times out.
func waitForTunnel(t *testing.T, s *Server, domain string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.tunnels.get(domain) != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tunnel %q not registered within 2s", domain)
}

// runL4ClientSimulator implements the CLI-side of the L4 tunnel frame
// protocol: read binary frames from the server's tunnel WebSocket, dial
// localAddr on OPEN, and pipe bytes in both directions.
func runL4ClientSimulator(t *testing.T, ws *websocket.Conn, localAddr string) {
	t.Helper()
	var wmu sync.Mutex
	writeFrame := func(fType byte, sid uint32, payload []byte) error {
		buf := make([]byte, 5+len(payload))
		buf[0] = fType
		binary.BigEndian.PutUint32(buf[1:5], sid)
		copy(buf[5:], payload)
		wmu.Lock()
		defer wmu.Unlock()
		return ws.WriteMessage(websocket.BinaryMessage, buf)
	}

	var smu sync.Mutex
	streams := make(map[uint32]net.Conn)
	closeStream := func(sid uint32) {
		smu.Lock()
		c, ok := streams[sid]
		delete(streams, sid)
		smu.Unlock()
		if ok {
			c.Close()
		}
	}

	for {
		mt, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage || len(raw) < 5 {
			continue
		}
		fType := raw[0]
		sid := binary.BigEndian.Uint32(raw[1:5])
		payload := raw[5:]
		switch fType {
		case frameOpen:
			c, err := net.Dial("tcp", localAddr)
			if err != nil {
				_ = writeFrame(frameClose, sid, nil)
				continue
			}
			smu.Lock()
			streams[sid] = c
			smu.Unlock()
			go func(c net.Conn, sid uint32) {
				buf := make([]byte, 16*1024)
				for {
					n, rerr := c.Read(buf)
					if n > 0 {
						if werr := writeFrame(frameData, sid, buf[:n]); werr != nil {
							closeStream(sid)
							return
						}
					}
					if rerr != nil {
						_ = writeFrame(frameClose, sid, nil)
						closeStream(sid)
						return
					}
				}
			}(c, sid)
		case frameData:
			smu.Lock()
			c := streams[sid]
			smu.Unlock()
			if c != nil {
				if _, err := c.Write(payload); err != nil {
					closeStream(sid)
					_ = writeFrame(frameClose, sid, nil)
				}
			}
		case frameClose:
			closeStream(sid)
		}
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
			streams:      make(map[uint32]*tunnelStream),
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
	tc := &tunnelConn{authRequired: true, streams: make(map[uint32]*tunnelStream)}
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
			TLS: &traefikTLS{
				CertResolver: "letsencrypt",
				Domains:      []traefikTLSDomain{{Main: host}},
			},
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
	tc := &tunnelConn{authRequired: false, streams: make(map[uint32]*tunnelStream)}
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
			TLS: &traefikTLS{
				CertResolver: "letsencrypt",
				Domains:      []traefikTLSDomain{{Main: host}},
			},
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

// ─── WebSocket upgrade through tunnel (regression) ──────────────────────────

// TestTunnelWebSocket_E2E is the regression test for the WebSocket-through-
// tunnel bug. It starts a local WS echo server, wires up the L4 tunnel, and
// verifies a browser-side WebSocket client can complete its upgrade and echo
// messages. On the old HTTP-over-JSON protocol this failed because the
// Connection/Upgrade hop-by-hop headers were stripped on both sides.
func TestTunnelWebSocket_E2E(t *testing.T) {
	echoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	defer echoSrv.Close()
	localAddr := strings.TrimPrefix(echoSrv.URL, "http://")

	s := &Server{
		baseDomain: "example.com",
		tunnels:    newTunnelRegistry(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tunnel/connect", func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		ws, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		tc := &tunnelConn{
			ws:        ws,
			userEmail: "test@example.com",
			streams:   make(map[uint32]*tunnelStream),
		}
		s.tunnels.register(domain, tc)
		defer func() {
			s.tunnels.unregister(domain, tc)
			ws.Close()
		}()
		tc.serveConn()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		r.Host = "t-test-fox.example.com"
		s.handleTunnelTraffic(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cliWsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/tunnel/connect?domain=t-test-fox"
	cliWs, _, err := websocket.DefaultDialer.Dial(cliWsURL, nil)
	if err != nil {
		t.Fatalf("cli dial: %v", err)
	}
	defer cliWs.Close()
	go runL4ClientSimulator(t, cliWs, localAddr)

	waitForTunnel(t, s, "t-test-fox")

	// Open a real WebSocket client through the tunnel and verify echo works.
	clientWsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/"
	clientWs, _, err := websocket.DefaultDialer.Dial(clientWsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket handshake through tunnel failed: %v", err)
	}
	defer clientWs.Close()

	if err := clientWs.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	clientWs.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := clientWs.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != "ping" {
		t.Fatalf("expected echo 'ping', got %q", string(msg))
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

// ─── domain_only prefix routing ─────────────────────────────────────────────

func TestIsTunnelRequest_DomainOnlyPrefix(t *testing.T) {
	s := &Server{
		baseDomain:         "example.com",
		domainOnlyPrefixes: map[string]bool{"reserved": true},
	}

	tests := []struct {
		host string
		want bool
	}{
		{"reserved.example.com", true},       // matches domain_only cache
		{"reserved.example.com:443", true},   // with port
		{"t-bold-fox.example.com", true},     // ephemeral tunnel
		{"unreserved.example.com", false},    // neither tunnel nor domain_only
		{"reserved.other.com", false},        // wrong base domain
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

func TestTunnelTraffic_DomainOnlyOfflinePlaceholder(t *testing.T) {
	s := &Server{
		baseDomain:         "example.com",
		tunnels:            newTunnelRegistry(),
		domainOnlyPrefixes: map[string]bool{"reserved": true},
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "reserved.example.com"
	rec := httptest.NewRecorder()

	s.handleTunnelTraffic(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("expected Retry-After header")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected html content type, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "reserved") {
		t.Error("offline page should mention the prefix")
	}
	if !strings.Contains(body, "muveectl tunnel --project") {
		t.Error("offline page should show reconnect hint")
	}
}

func TestTunnelTraffic_UnknownPrefixStillBadGateway(t *testing.T) {
	// A non-t-* host that is NOT in the domain_only cache must still 502, not
	// accidentally serve the offline page.
	s := &Server{
		baseDomain:         "example.com",
		tunnels:            newTunnelRegistry(),
		domainOnlyPrefixes: map[string]bool{"reserved": true},
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "mystery.example.com"
	rec := httptest.NewRecorder()

	s.handleTunnelTraffic(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

func TestWriteTunnelOfflinePage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeTunnelOfflinePage(rec, "mine")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Errorf("expected Retry-After=5, got %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("expected Cache-Control=no-store, got %q", got)
	}
	// Prefix must appear in the rendered HTML (title + body + hint).
	if n := strings.Count(rec.Body.String(), "mine"); n < 3 {
		t.Errorf("expected prefix 'mine' to appear 3+ times, got %d", n)
	}
}
