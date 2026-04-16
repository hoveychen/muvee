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

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
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
	// projectName is the optional domain_only project name this tunnel is
	// bound to. Empty for ephemeral t-* tunnels.
	projectName string

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

// register inserts tc into the registry under domain. Returns false if another
// live tunnel already owns the domain (first-come-first-served). Callers that
// successfully register must pair with unregister on disconnect.
func (tr *tunnelRegistry) register(domain string, tc *tunnelConn) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.tunnels[domain]; ok {
		return false
	}
	tr.tunnels[domain] = tc
	return true
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
	ProjectName  string    `json:"project_name"`
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
			ProjectName:  tc.projectName,
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
//
// Two mutually exclusive modes:
//   - domain=<prefix>: ephemeral tunnel, prefix must match the t-word-word
//     format. The domain is not persisted; it's gone when the socket closes.
//   - project=<name>: project-scoped tunnel, prefix is the domain_prefix of a
//     domain_only project owned by the caller. The reservation survives even
//     when no tunnel is live (handleTunnelTraffic serves an offline placeholder).
//
// Both modes are first-come-first-served: a second connect while a tunnel is
// already live for the same prefix is rejected with 409.
func (s *Server) handleTunnelConnect(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())

	rawDomain := r.URL.Query().Get("domain")
	projectName := r.URL.Query().Get("project")
	if rawDomain != "" && projectName != "" {
		http.Error(w, "domain and project parameters are mutually exclusive", http.StatusBadRequest)
		return
	}

	var (
		domain      string
		boundProject string
	)
	switch {
	case projectName != "":
		proj, err := s.store.GetProjectByOwnerAndName(r.Context(), user.ID, projectName)
		if err != nil {
			http.Error(w, "project lookup failed", http.StatusInternalServerError)
			return
		}
		if proj == nil {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		if proj.ProjectType != store.ProjectTypeDomainOnly {
			http.Error(w, "project is not a domain_only project", http.StatusConflict)
			return
		}
		domain = proj.DomainPrefix
		boundProject = proj.Name
	case rawDomain != "":
		if !isValidTunnelDomain(rawDomain) {
			http.Error(w, "invalid domain parameter", http.StatusBadRequest)
			return
		}
		domain = rawDomain
	default:
		http.Error(w, "domain or project parameter is required", http.StatusBadRequest)
		return
	}

	// By default tunnels require ForwardAuth; the CLI sends noauth=1 to opt out.
	authRequired := r.URL.Query().Get("noauth") != "1"

	// First-come-first-served: reject early before the WS upgrade so the
	// second client gets a proper HTTP 409 instead of a surprise WS close.
	if s.tunnels.get(domain) != nil {
		http.Error(w, "a tunnel is already connected to this domain", http.StatusConflict)
		return
	}

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
		projectName:  boundProject,
		streams:      make(map[uint32]*tunnelStream),
	}

	if !s.tunnels.register(domain, tc) {
		// Lost the race between the pre-upgrade check and the register call.
		ws.Close()
		return
	}

	historyID := s.recordTunnelConnect(domain, user.Email, authRequired)
	tc.historyID = historyID

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

	// Record tunnel traffic for project-bound tunnels asynchronously.
	s.recordTunnelTraffic(r, domain)

	tc := s.tunnels.get(domain)
	if tc == nil {
		// If this prefix belongs to a domain_only project, the reservation
		// outlives the tunnel connection — serve a friendly placeholder so the
		// user can confirm the domain is correct and the service is just down.
		if s.isDomainOnlyPrefix(domain) {
			writeTunnelOfflinePage(w, domain)
			return
		}
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
// It matches both ephemeral t-* tunnels and registered domain_only projects,
// so that a request for a reserved prefix is routed through the tunnel handler
// (and gets either live traffic or the offline placeholder).
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
	if strings.HasPrefix(prefix, "t-") {
		return true
	}
	return s.isDomainOnlyPrefix(prefix)
}

// tunnelOfflineHTML is served for a domain_only project whose reserved prefix
// has no live tunnel. It reassures the user that the domain is correct and the
// service is simply down rather than missing.
const tunnelOfflineHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Service Offline — %s</title>
<style>
html,body{height:100%%;margin:0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#0f172a;color:#e2e8f0}
.wrap{min-height:100%%;display:flex;align-items:center;justify-content:center;padding:2rem}
.card{max-width:32rem;text-align:center;padding:2.5rem 2rem;background:#1e293b;border:1px solid #334155;border-radius:12px;box-shadow:0 20px 40px rgba(0,0,0,.35)}
.dot{display:inline-block;width:.7rem;height:.7rem;border-radius:50%%;background:#f59e0b;margin-right:.5rem;vertical-align:middle}
h1{margin:0 0 .75rem;font-size:1.5rem;font-weight:600}
p{margin:.5rem 0;color:#94a3b8;line-height:1.55}
code{background:#0f172a;border:1px solid #334155;padding:.1rem .45rem;border-radius:4px;color:#e2e8f0;font-size:.95em}
</style>
</head>
<body>
<div class="wrap"><div class="card">
<h1><span class="dot"></span>Service offline</h1>
<p>The reserved domain <code>%s</code> is correct,<br>but nothing is currently tunneling to it.</p>
<p>If this is your project, start the tunnel with<br><code>muveectl tunnel --project %s &lt;PORT&gt;</code>.</p>
</div></div>
</body>
</html>`

// writeTunnelOfflinePage renders the offline placeholder for a reserved
// domain_only prefix with no live tunnel. Responds 503 + Retry-After so HTTP
// monitors see a retryable "service unavailable" state rather than a hard 404.
func writeTunnelOfflinePage(w http.ResponseWriter, domain string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "5")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, tunnelOfflineHTML, domain, domain, domain)
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

// recordTunnelTraffic asynchronously inserts a project_traffic row for
// project-bound tunnel requests. Ephemeral t-* tunnels with no project
// association are skipped because there is no project_id to key on.
func (s *Server) recordTunnelTraffic(r *http.Request, domain string) {
	if s.store == nil {
		return
	}
	projectID, err := s.store.ResolveProjectIDByDomainPrefix(r.Context(), domain)
	if err != nil || projectID == (uuid.UUID{}) {
		return
	}
	clientIP := r.RemoteAddr
	if idx := strings.LastIndex(clientIP, ":"); idx != -1 {
		clientIP = clientIP[:idx]
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		clientIP = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	t := &store.ProjectTraffic{
		ProjectID:  projectID,
		ObservedAt: time.Now(),
		ClientIP:   clientIP,
		Host:       r.Host,
		Method:     r.Method,
		Path:       r.URL.RequestURI(),
		Status:     0, // L4 tunnel — response status is not observable
		DurationMs: 0,
		BytesSent:  0,
		UserAgent:  r.Header.Get("User-Agent"),
		Referer:    r.Header.Get("Referer"),
	}
	go func() {
		_ = s.store.InsertProjectTraffic(context.Background(), t)
	}()
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
