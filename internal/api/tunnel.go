package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoveychen/muvee/internal/auth"
)

// ─── Tunnel message protocol ────────────────────────────────────────────────

type tunnelMsg struct {
	Type       string              `json:"type"`               // "request" or "response"
	ID         string              `json:"id"`                 // unique per-request
	Method     string              `json:"method,omitempty"`   // request only
	Path       string              `json:"path,omitempty"`     // request only (path + query)
	StatusCode int                 `json:"status,omitempty"`   // response only
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       string              `json:"body,omitempty"`     // base64-encoded
}

// ─── Tunnel registry ────────────────────────────────────────────────────────

type tunnelConn struct {
	ws           *websocket.Conn
	userEmail    string
	authRequired bool      // whether ForwardAuth is enabled for this tunnel
	connectedAt  time.Time // when the tunnel was established
	historyID    string    // tunnel_history row ID (for updating disconnected_at)
	mu           sync.Mutex                // guards ws writes
	pending      map[string]chan *tunnelMsg // requestID → response channel
	pendingMu    sync.Mutex
}

func (tc *tunnelConn) writeJSON(v interface{}) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.ws.WriteJSON(v)
}

func (tc *tunnelConn) addPending(id string) chan *tunnelMsg {
	ch := make(chan *tunnelMsg, 1)
	tc.pendingMu.Lock()
	tc.pending[id] = ch
	tc.pendingMu.Unlock()
	return ch
}

func (tc *tunnelConn) removePending(id string) {
	tc.pendingMu.Lock()
	delete(tc.pending, id)
	tc.pendingMu.Unlock()
}

func (tc *tunnelConn) dispatchResponse(msg *tunnelMsg) {
	tc.pendingMu.Lock()
	ch, ok := tc.pending[msg.ID]
	tc.pendingMu.Unlock()
	if ok {
		ch <- msg
	}
}

// tunnelRegistry tracks active WebSocket tunnels keyed by domain prefix.
type tunnelRegistry struct {
	mu      sync.RWMutex
	tunnels map[string]*tunnelConn // domain prefix → connection
}

func newTunnelRegistry() *tunnelRegistry {
	return &tunnelRegistry{tunnels: make(map[string]*tunnelConn)}
}

func (tr *tunnelRegistry) register(domain string, tc *tunnelConn) {
	tr.mu.Lock()
	// Close any existing tunnel for the same domain.
	if old, ok := tr.tunnels[domain]; ok && old.ws != nil {
		old.ws.Close()
	}
	tr.tunnels[domain] = tc
	tr.mu.Unlock()
}

func (tr *tunnelRegistry) unregister(domain string, tc *tunnelConn) {
	tr.mu.Lock()
	if cur, ok := tr.tunnels[domain]; ok && cur == tc {
		delete(tr.tunnels, domain)
	}
	tr.mu.Unlock()
}

func (tr *tunnelRegistry) get(domain string) *tunnelConn {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.tunnels[domain]
}

// tunnelInfo is a snapshot of an active tunnel.
type tunnelInfo struct {
	Domain       string    `json:"domain"`
	UserEmail    string    `json:"user_email"`
	AuthRequired bool      `json:"auth_required"`
	ConnectedAt  time.Time `json:"connected_at"`
}

// activeTunnels returns a snapshot of all currently registered tunnels.
func (tr *tunnelRegistry) activeTunnels() []tunnelInfo {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]tunnelInfo, 0, len(tr.tunnels))
	for d, tc := range tr.tunnels {
		out = append(out, tunnelInfo{
			Domain:       d,
			UserEmail:    tc.userEmail,
			AuthRequired: tc.authRequired,
			ConnectedAt:  tc.connectedAt,
		})
	}
	return out
}

// ─── WebSocket upgrade ──────────────────────────────────────────────────────

var wsUpgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return true },
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
}

// handleTunnelConnect upgrades the connection to a WebSocket and registers a tunnel.
// Route: GET /api/tunnel/connect?domain=t-happy-fox  (authenticated)
func (s *Server) handleTunnelConnect(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" || !isValidTunnelDomain(domain) {
		http.Error(w, "invalid or missing domain parameter", http.StatusBadRequest)
		return
	}

	user := auth.UserFromCtx(r.Context())

	// By default tunnels require ForwardAuth; the CLI sends noauth=1 to opt out.
	authRequired := r.URL.Query().Get("noauth") != "1"

	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("tunnel ws upgrade: %v", err)
		return
	}

	// Configure Ping/Pong keepalive.  The server sends a Ping every 30s;
	// if no Pong is received within 45s the connection is considered dead.
	const (
		pingInterval = 30 * time.Second
		pongTimeout  = 45 * time.Second
	)
	ws.SetReadDeadline(time.Now().Add(pongTimeout))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	now := time.Now()
	tc := &tunnelConn{
		ws:           ws,
		userEmail:    user.Email,
		authRequired: authRequired,
		connectedAt:  now,
		pending:      make(map[string]chan *tunnelMsg),
	}

	// Record tunnel connection in DB history.
	historyID := s.recordTunnelConnect(domain, user.Email, authRequired)
	tc.historyID = historyID

	s.tunnels.register(domain, tc)
	log.Printf("Tunnel registered: %s.%s (user: %s)", domain, s.baseDomain, user.Email)
	defer func() {
		s.tunnels.unregister(domain, tc)
		ws.Close()
		s.recordTunnelDisconnect(historyID)
		log.Printf("Tunnel closed: %s.%s", domain, s.baseDomain)
	}()

	// Ping ticker — runs in a separate goroutine.
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				tc.mu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				tc.mu.Unlock()
				if err != nil {
					return
				}
			case <-pingDone:
				return
			}
		}
	}()
	defer close(pingDone)

	// Read loop: dispatch response messages back to pending HTTP handlers.
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			if !websocket.IsUnexpectedCloseError(err) {
				return
			}
			log.Printf("tunnel read error (%s): %v", domain, err)
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
}

// ─── Tunnel HTTP proxy ──────────────────────────────────────────────────────

// handleTunnelTraffic proxies an incoming HTTP request through the tunnel WebSocket
// to the CLI client's local service.  It is called when the request's Host header
// matches a registered tunnel domain.
func (s *Server) handleTunnelTraffic(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	// Strip port if present.
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	// Extract the domain prefix (everything before .baseDomain).
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		http.Error(w, "unknown tunnel host", http.StatusBadGateway)
		return
	}
	domain := strings.TrimSuffix(host, suffix)

	tc := s.tunnels.get(domain)
	if tc == nil {
		http.Error(w, "tunnel not connected", http.StatusBadGateway)
		return
	}

	// Serialize the incoming HTTP request.
	reqID := uuid.New().String()
	path := r.URL.Path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}

	var bodyB64 string
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20))
		if err == nil && len(body) > 0 {
			bodyB64 = base64.StdEncoding.EncodeToString(body)
		}
	}

	// Copy headers, skip hop-by-hop.
	headers := make(map[string][]string)
	for k, vv := range r.Header {
		kl := strings.ToLower(k)
		if kl == "connection" || kl == "upgrade" || kl == "transfer-encoding" {
			continue
		}
		headers[k] = vv
	}

	msg := tunnelMsg{
		Type:    "request",
		ID:      reqID,
		Method:  r.Method,
		Path:    path,
		Headers: headers,
		Body:    bodyB64,
	}

	// Register a pending response channel before sending.
	ch := tc.addPending(reqID)
	defer tc.removePending(reqID)

	if err := tc.writeJSON(msg); err != nil {
		http.Error(w, "tunnel send error", http.StatusBadGateway)
		return
	}

	// Wait for the response from the CLI (timeout: 120s).
	select {
	case resp := <-ch:
		// Write response headers.
		for k, vv := range resp.Headers {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if resp.Body != "" {
			body, err := base64.StdEncoding.DecodeString(resp.Body)
			if err == nil {
				w.Write(body)
			}
		}
	case <-time.After(120 * time.Second):
		http.Error(w, "tunnel timeout", http.StatusGatewayTimeout)
	case <-r.Context().Done():
		// Client disconnected.
	}
}

// isTunnelRequest returns true if the Host header matches a tunnel domain pattern.
func (s *Server) isTunnelRequest(r *http.Request) bool {
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	prefix := strings.TrimSuffix(host, suffix)
	return strings.HasPrefix(prefix, "t-")
}

// ─── Tunnel history (DB) ────────────────────────────────────────────────────

func (s *Server) recordTunnelConnect(domain, email string, authRequired bool) string {
	var id string
	err := s.store.DB().QueryRow(context.Background(),
		`INSERT INTO tunnel_history (domain, user_email, auth_required) VALUES ($1, $2, $3) RETURNING id`,
		domain, email, authRequired,
	).Scan(&id)
	if err != nil {
		log.Printf("tunnel history insert: %v", err)
		return ""
	}
	return id
}

func (s *Server) recordTunnelDisconnect(historyID string) {
	if historyID == "" {
		return
	}
	_, err := s.store.DB().Exec(context.Background(),
		`UPDATE tunnel_history SET disconnected_at = now() WHERE id = $1`, historyID)
	if err != nil {
		log.Printf("tunnel history update: %v", err)
	}
}

// tunnelHistoryEntry is a row from tunnel_history for the admin API.
type tunnelHistoryEntry struct {
	ID             string  `json:"id"`
	Domain         string  `json:"domain"`
	UserEmail      string  `json:"user_email"`
	AuthRequired   bool    `json:"auth_required"`
	ConnectedAt    string  `json:"connected_at"`
	DisconnectedAt *string `json:"disconnected_at"`
}

// ─── Admin API handlers ─────────────────────────────────────────────────────

func (s *Server) listActiveTunnels(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.tunnels.activeTunnels())
}

func (s *Server) listTunnelHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB().Query(r.Context(),
		`SELECT id, domain, user_email, auth_required, connected_at, disconnected_at
		 FROM tunnel_history ORDER BY connected_at DESC LIMIT 100`)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	defer rows.Close()

	var entries []tunnelHistoryEntry
	for rows.Next() {
		var e tunnelHistoryEntry
		var connAt time.Time
		var discAt *time.Time
		if err := rows.Scan(&e.ID, &e.Domain, &e.UserEmail, &e.AuthRequired, &connAt, &discAt); err != nil {
			continue
		}
		e.ConnectedAt = connAt.Format(time.RFC3339)
		if discAt != nil {
			s := discAt.Format(time.RFC3339)
			e.DisconnectedAt = &s
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []tunnelHistoryEntry{}
	}
	jsonOK(w, entries)
}

// isValidTunnelDomain validates the domain prefix format (t-word-word).
func isValidTunnelDomain(domain string) bool {
	if !strings.HasPrefix(domain, "t-") {
		return false
	}
	parts := strings.Split(domain[2:], "-")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 {
			return false
		}
		for _, c := range p {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
				return false
			}
		}
	}
	return true
}
