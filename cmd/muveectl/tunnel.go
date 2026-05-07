package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

// ─── Tunnel command ──────────────────────────────────────────────────────────

var tunnelCmd = &cobra.Command{
	Use:   "tunnel PORT",
	Short: "Publish a local port to the internet via tunnel",
	Long: `Publish a local port directly to the internet — no deployment, no Docker, no git repo required.
The domain is deterministically generated from the current working directory and port number,
so reconnecting from the same directory reuses the same URL.

Use --project <name> to mount the tunnel on a reserved domain_only project's
domain_prefix instead of an ephemeral t-* name.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		domain, _ := cmd.Flags().GetString("domain")
		project, _ := cmd.Flags().GetString("project")
		noAuth, _ := cmd.Flags().GetBool("no-auth")
		return cmdTunnel(args[0], domain, project, noAuth, cl)
	},
}

func init() {
	tunnelCmd.Flags().String("domain", "", "Override auto-generated domain prefix")
	tunnelCmd.Flags().String("project", "", "Mount tunnel on a reserved domain_only project (by name)")
	tunnelCmd.Flags().Bool("no-auth", false, "Disable ForwardAuth (public access)")
	tunnelCmd.MarkFlagsMutuallyExclusive("domain", "project")
	rootCmd.AddCommand(tunnelCmd)
}

// ─── L4 tunnel protocol ─────────────────────────────────────────────────────
//
// The tunnel multiplexes raw TCP streams from muvee-server over a single
// WebSocket. Each binary message is one frame:
//
//	[type:1][streamID:4 BE][payload...]
//
// frameOpen  (1): server asks the CLI to dial the local service for a new stream.
// frameData  (2): raw byte chunk; the CLI writes it to the local conn (or
//                 the server writes it to the browser-side hijacked conn).
// frameClose (3): either side closes the stream.
const (
	frameOpen  byte = 1
	frameData  byte = 2
	frameClose byte = 3
)

// wsMutexWriter wraps a *websocket.Conn with a mutex so concurrent writes
// from multiple stream goroutines are safe.
type wsMutexWriter struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (w *wsMutexWriter) writeMessage(msgType int, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ws.WriteMessage(msgType, data)
}

func (w *wsMutexWriter) writeControl(msgType int, data []byte, deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ws.WriteControl(msgType, data, deadline)
}

func (w *wsMutexWriter) writeFrame(fType byte, streamID uint32, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = fType
	binary.BigEndian.PutUint32(buf[1:5], streamID)
	copy(buf[5:], payload)
	return w.writeMessage(websocket.BinaryMessage, buf)
}

func cmdTunnel(port, customDomain, projectName string, noAuth bool, c *client) error {
	localAddr := "127.0.0.1:" + port

	rc, err := c.do("GET", "/api/runtime/config", nil)
	if err != nil {
		return fmt.Errorf("fetch runtime config: %w", err)
	}
	baseDomain, _ := rc["base_domain"].(string)
	if baseDomain == "" {
		baseDomain = "localhost"
	}

	// Resolve domain for the public URL banner. For --project we fetch the
	// project's current domain_prefix up front so the displayed URL is accurate;
	// the server still enforces ownership and type at handshake time.
	var (
		domain string
		mode   string
	)
	switch {
	case projectName != "":
		mode = "project"
		items, lerr := c.doArray("GET", "/api/projects", nil)
		if lerr != nil {
			return fmt.Errorf("fetch projects: %w", lerr)
		}
		var matched map[string]interface{}
		for _, it := range items {
			m, _ := it.(map[string]interface{})
			if str(m, "name") == projectName {
				matched = m
				break
			}
		}
		if matched == nil {
			return fmt.Errorf("project %q not found", projectName)
		}
		if str(matched, "project_type") != "domain_only" {
			return fmt.Errorf("project %q is not a domain_only project", projectName)
		}
		domain = str(matched, "domain_prefix")
		log.Printf("tunnel: resolved mode=project project=%q project_id=%q domain_prefix=%q",
			projectName, str(matched, "id"), domain)
	case customDomain != "":
		mode = "custom"
		domain = customDomain
		log.Printf("tunnel: resolved mode=custom domain=%q", domain)
	default:
		mode = "hash"
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		portNum := 0
		fmt.Sscanf(port, "%d", &portNum)
		domain = tunnelDomain(cwd, portNum)
		log.Printf("tunnel: resolved mode=hash cwd=%q port=%d domain=%q", cwd, portNum, domain)
	}
	_ = mode

	publicURL := fmt.Sprintf("https://%s.%s", domain, baseDomain)

	wsScheme := "wss"
	serverURL, _ := url.Parse(c.server)
	if serverURL != nil && serverURL.Scheme == "http" {
		wsScheme = "ws"
	}
	wsHost := serverURL.Host
	var wsURL string
	if projectName != "" {
		wsURL = fmt.Sprintf("%s://%s/api/tunnel/connect?project=%s", wsScheme, wsHost, url.QueryEscape(projectName))
	} else {
		wsURL = fmt.Sprintf("%s://%s/api/tunnel/connect?domain=%s", wsScheme, wsHost, url.QueryEscape(domain))
	}
	if noAuth {
		wsURL += "&noauth=1"
	}

	wsHeader := http.Header{}
	wsHeader.Set("Authorization", "Bearer "+c.token)
	log.Printf("tunnel: ws url=%s local=%s public=%s", wsURL, localAddr, publicURL)

	authLabel := "on (ForwardAuth)"
	if noAuth {
		authLabel = "off (public)"
	}
	fmt.Printf("Tunnel:\n")
	fmt.Printf("  %s → %s\n", publicURL, localAddr)
	fmt.Printf("  Domain: %s\n", domain)
	fmt.Printf("  Auth:   %s\n", authLabel)
	fmt.Println("Press Ctrl+C to stop.")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	backoff := time.Second
	const (
		maxBackoff    = 30 * time.Second
		stableSession = 30 * time.Second
	)

	for {
		sessionStart := time.Now()
		err := tunnelSession(ctx, wsURL, wsHeader, localAddr)
		if ctx.Err() != nil {
			fmt.Println("\nTunnel stopped.")
			return nil
		}
		if err != nil {
			log.Printf("tunnel disconnected: %v", err)
		}
		// If the session lasted long enough, it was healthy — this drop is a
		// fresh incident, not part of a failure streak, so reset the backoff.
		if time.Since(sessionStart) >= stableSession {
			backoff = time.Second
		}
		fmt.Printf("Reconnecting in %s...\n", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			fmt.Println("\nTunnel stopped.")
			return nil
		}
		backoff = backoff * 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// tunnelSession runs a single WebSocket connection. It returns when the
// connection is lost, closed, or ctx is canceled. The caller decides whether
// to reconnect.
func tunnelSession(ctx context.Context, wsURL string, header http.Header, localAddr string) error {
	const pongTimeout = 45 * time.Second

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   32 * 1024,
		WriteBufferSize:  32 * 1024,
	}
	ws, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ws.Close()

	// Server pings keep refreshing the read deadline, so ReadMessage below
	// would otherwise block forever on Ctrl+C. Close the ws when ctx is
	// canceled so ReadMessage returns and the outer loop sees ctx.Err().
	sessionDone := make(chan struct{})
	defer close(sessionDone)
	go func() {
		select {
		case <-ctx.Done():
			ws.Close()
		case <-sessionDone:
		}
	}()

	writer := &wsMutexWriter{ws: ws}

	ws.SetReadDeadline(time.Now().Add(pongTimeout))
	ws.SetPingHandler(func(msg string) error {
		ws.SetReadDeadline(time.Now().Add(pongTimeout))
		return writer.writeControl(websocket.PongMessage, []byte(msg), time.Now().Add(10*time.Second))
	})

	fmt.Println("Connected.")

	// Per-stream state. Each stream owns a dedicated writer goroutine that
	// drains inbox and synchronously writes to the local conn. The main read
	// loop only dispatches frames into the inbox, so a stuck local consumer
	// never stalls other streams sharing this tunnel.
	type cliStream struct {
		conn      net.Conn
		inbox     chan []byte
		done      chan struct{}
		closeOnce sync.Once
	}
	closeS := func(s *cliStream) {
		s.closeOnce.Do(func() {
			close(s.done)
			s.conn.Close()
		})
	}
	var streamsMu sync.Mutex
	streams := make(map[uint32]*cliStream)

	closeStream := func(sid uint32) {
		streamsMu.Lock()
		s, ok := streams[sid]
		delete(streams, sid)
		streamsMu.Unlock()
		if ok {
			closeS(s)
		}
	}

	defer func() {
		streamsMu.Lock()
		for _, s := range streams {
			closeS(s)
		}
		streams = map[uint32]*cliStream{}
		streamsMu.Unlock()
	}()

	for {
		msgType, raw, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
		if msgType != websocket.BinaryMessage || len(raw) < 5 {
			continue
		}
		fType := raw[0]
		sid := binary.BigEndian.Uint32(raw[1:5])
		payload := raw[5:]

		switch fType {
		case frameOpen:
			log.Printf("tunnel: frame OPEN sid=%d → dial %s", sid, localAddr)
			c, err := net.Dial("tcp", localAddr)
			if err != nil {
				log.Printf("tunnel: dial FAIL sid=%d addr=%s err=%v", sid, localAddr, err)
				_ = writer.writeFrame(frameClose, sid, nil)
				continue
			}
			log.Printf("tunnel: dial OK sid=%d local=%s remote=%s", sid, c.LocalAddr(), c.RemoteAddr())
			s := &cliStream{
				conn:  c,
				inbox: make(chan []byte, 1024),
				done:  make(chan struct{}),
			}
			streamsMu.Lock()
			streams[sid] = s
			streamsMu.Unlock()

			// local → tunnel
			go func(c net.Conn, sid uint32) {
				buf := make([]byte, 16*1024)
				for {
					n, rerr := c.Read(buf)
					if n > 0 {
						if werr := writer.writeFrame(frameData, sid, buf[:n]); werr != nil {
							closeStream(sid)
							return
						}
					}
					if rerr != nil {
						_ = writer.writeFrame(frameClose, sid, nil)
						closeStream(sid)
						return
					}
				}
			}(c, sid)

			// tunnel → local: dedicated per-stream writer so a stalled
			// c.Write does not freeze the WS read loop for other streams.
			go func(s *cliStream, sid uint32) {
				for {
					select {
					case data := <-s.inbox:
						if _, err := s.conn.Write(data); err != nil {
							_ = writer.writeFrame(frameClose, sid, nil)
							closeStream(sid)
							return
						}
					case <-s.done:
						return
					}
				}
			}(s, sid)

		case frameData:
			streamsMu.Lock()
			s := streams[sid]
			streamsMu.Unlock()
			if s != nil {
				// Copy payload since the WebSocket read buffer is reused.
				data := make([]byte, len(payload))
				copy(data, payload)
				// Non-blocking dispatch: if the inbox is full the local
				// consumer is hopelessly behind, so abort the stream rather
				// than block the shared read loop and freeze every other
				// stream on this tunnel.
				select {
				case s.inbox <- data:
				case <-s.done:
				default:
					log.Printf("tunnel: frame DROP sid=%d reason=inbox_full", sid)
					_ = writer.writeFrame(frameClose, sid, nil)
					closeStream(sid)
				}
			}

		case frameClose:
			log.Printf("tunnel: frame CLOSE sid=%d", sid)
			closeStream(sid)
		}
	}
}
