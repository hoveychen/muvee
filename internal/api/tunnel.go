package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hoveychen/muvee/internal/auth"
)

// ─── L4 tunnel frame protocol ───────────────────────────────────────────────
//
// Each binary WebSocket message between muvee-server and muveectl is one
// frame, encoded as:
//
//	[type:1][streamID:4 BE][payload...]
//
// Types:
//   - frameOpen  (1): server asks CLI to dial the local service. Payload empty.
//   - frameData  (2): raw byte chunk for an existing stream.
//   - frameClose (3): one side is closing the stream. Payload empty.
//
// Streams multiplex multiple concurrent browser connections over a single
// tunnel WebSocket. The server allocates stream IDs starting at 1.
const (
	frameOpen  byte = 1
	frameData  byte = 2
	frameClose byte = 3
)

// ─── Tunnel registry ────────────────────────────────────────────────────────

type tunnelStream struct {
	id        uint32
	inbox     chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func (ts *tunnelStream) close() {
	ts.closeOnce.Do(func() { close(ts.closed) })
}

type tunnelConn struct {
	ws           *websocket.Conn
	userEmail    string
	authRequired bool
	connectedAt  time.Time
	historyID    string

	mu sync.Mutex // guards ws writes

	streamMu     sync.Mutex
	streams      map[uint32]*tunnelStream
	nextStreamID uint32
}

func (tc *tunnelConn) writeFrame(fType byte, streamID uint32, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = fType
	binary.BigEndian.PutUint32(buf[1:5], streamID)
	copy(buf[5:], payload)
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.ws.WriteMessage(websocket.BinaryMessage, buf)
}

func (tc *tunnelConn) newStream() *tunnelStream {
	tc.streamMu.Lock()
	defer tc.streamMu.Unlock()
	tc.nextStreamID++
	s := &tunnelStream{
		id:     tc.nextStreamID,
		inbox:  make(chan []byte, 32),
		closed: make(chan struct{}),
	}
	tc.streams[s.id] = s
	return s
}

func (tc *tunnelConn) getStream(id uint32) *tunnelStream {
	tc.streamMu.Lock()
	defer tc.streamMu.Unlock()
	return tc.streams[id]
}

func (tc *tunnelConn) removeStream(id uint32) {
	tc.streamMu.Lock()
	s, ok := tc.streams[id]
	delete(tc.streams, id)
	tc.streamMu.Unlock()
	if ok {
		s.close()
	}
}

func (tc *tunnelConn) closeAllStreams() {
	tc.streamMu.Lock()
	for _, s := range tc.streams {
		s.close()
	}
	tc.streams = make(map[uint32]*tunnelStream)
	tc.streamMu.Unlock()
}

// serveConn runs the server-side frame read loop until the WebSocket is closed.
// It dispatches incoming DATA frames to the matching stream's inbox and closes
// streams on CLOSE frames or connection error.
func (tc *tunnelConn) serveConn() {
	defer tc.closeAllStreams()
	for {
		msgType, raw, err := tc.ws.ReadMessage()
		if err != nil {
			return
		}
		if msgType != websocket.BinaryMessage || len(raw) < 5 {
			continue
		}
		fType := raw[0]
		sid := binary.BigEndian.Uint32(raw[1:5])
		payload := raw[5:]
		stream := tc.getStream(sid)
		if stream == nil {
			continue
		}
		switch fType {
		case frameData:
			// Copy payload — the WebSocket buffer is reused across reads.
			data := make([]byte, len(payload))
			copy(data, payload)
			select {
			case stream.inbox <- data:
			case <-stream.closed:
			}
		case frameClose:
			tc.removeStream(sid)
		}
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
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
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
		streams:      make(map[uint32]*tunnelStream),
	}

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

	tc.serveConn()
}

// ─── Tunnel HTTP proxy (L4 passthrough via Hijack) ──────────────────────────

// handleTunnelTraffic hijacks the incoming HTTP connection and pipes raw bytes
// through the tunnel WebSocket to the CLI client's local service. Because the
// forwarding is byte-level, any protocol that runs over HTTP (including
// WebSocket upgrades) works transparently.
func (s *Server) handleTunnelTraffic(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
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

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}

	// Reconstruct the original HTTP/1.1 request bytes. The net/http server has
	// already consumed the request line and headers from the wire; we need to
	// replay them to the local service verbatim so that Connection/Upgrade and
	// friends survive the trip.
	//
	// For non-empty bodies we read via r.Body (which handles chunked decoding)
	// and emit a fixed Content-Length. For WebSocket upgrades the body is
	// empty so this is a no-op.
	const maxBody = 50 << 20
	body, err := readRequestBody(r, maxBody)
	if err != nil {
		http.Error(w, "request body read error", http.StatusBadRequest)
		return
	}

	var reqBuf bytes.Buffer
	fmt.Fprintf(&reqBuf, "%s %s HTTP/1.1\r\n", r.Method, r.URL.RequestURI())
	fmt.Fprintf(&reqBuf, "Host: %s\r\n", r.Host)
	for k, vv := range r.Header {
		if strings.EqualFold(k, "Host") ||
			strings.EqualFold(k, "Content-Length") ||
			strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vv {
			fmt.Fprintf(&reqBuf, "%s: %s\r\n", k, v)
		}
	}
	if len(body) > 0 {
		fmt.Fprintf(&reqBuf, "Content-Length: %d\r\n", len(body))
	}
	reqBuf.WriteString("\r\n")
	reqBuf.Write(body)

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = bufrw.Flush()

	stream := tc.newStream()
	defer tc.removeStream(stream.id)

	if err := tc.writeFrame(frameOpen, stream.id, nil); err != nil {
		return
	}
	// Send the reconstructed request in 16KB chunks so one huge frame doesn't
	// blow the WebSocket read buffer on the CLI side.
	for off := 0; off < reqBuf.Len(); {
		end := off + 16*1024
		if end > reqBuf.Len() {
			end = reqBuf.Len()
		}
		if err := tc.writeFrame(frameData, stream.id, reqBuf.Bytes()[off:end]); err != nil {
			return
		}
		off = end
	}

	// browser → tunnel: pipe everything after the headers+body we already
	// replayed. bufrw.Reader transparently handles whatever bytes were
	// pre-buffered by the HTTP server before Hijack.
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		buf := make([]byte, 16*1024)
		for {
			n, err := bufrw.Reader.Read(buf)
			if n > 0 {
				if werr := tc.writeFrame(frameData, stream.id, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// tunnel → browser
	for {
		select {
		case data := <-stream.inbox:
			if _, err := conn.Write(data); err != nil {
				_ = tc.writeFrame(frameClose, stream.id, nil)
				return
			}
		case <-stream.closed:
			return
		case <-clientDone:
			_ = tc.writeFrame(frameClose, stream.id, nil)
			return
		}
	}
}

// readRequestBody reads up to max bytes from r.Body and returns them.
func readRequestBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r.Body, max))
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
