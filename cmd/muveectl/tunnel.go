package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
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
so reconnecting from the same directory reuses the same URL.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		domain, _ := cmd.Flags().GetString("domain")
		noAuth, _ := cmd.Flags().GetBool("no-auth")
		return cmdTunnel(args[0], domain, noAuth, cl)
	},
}

func init() {
	tunnelCmd.Flags().String("domain", "", "Override auto-generated domain prefix")
	tunnelCmd.Flags().Bool("no-auth", false, "Disable ForwardAuth (public access)")
	rootCmd.AddCommand(tunnelCmd)
}

// ─── Tunnel implementation ───────────────────────────────────────────────────

// tunnelMsg is the wire format for HTTP-over-WebSocket tunnel communication.
type tunnelMsg struct {
	Type       string              `json:"type"`               // "request" or "response"
	ID         string              `json:"id"`                 // unique per-request
	Method     string              `json:"method,omitempty"`   // request only
	Path       string              `json:"path,omitempty"`     // request only (path + query)
	StatusCode int                 `json:"status,omitempty"`   // response only
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       string              `json:"body,omitempty"`     // base64-encoded
}

// wsMutexWriter wraps a *websocket.Conn with a mutex to make concurrent writes safe.
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

func cmdTunnel(port, customDomain string, noAuth bool, c *client) error {
	// Compute deterministic domain from CWD + port.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	domain := customDomain
	if domain == "" {
		portNum := 0
		fmt.Sscanf(port, "%d", &portNum)
		domain = tunnelDomain(cwd, portNum)
	}

	localTarget := "http://127.0.0.1:" + port

	// Fetch base domain from server.
	rc, err := c.do("GET", "/api/runtime/config", nil)
	if err != nil {
		return fmt.Errorf("fetch runtime config: %w", err)
	}
	baseDomain, _ := rc["base_domain"].(string)
	if baseDomain == "" {
		baseDomain = "localhost"
	}
	publicURL := fmt.Sprintf("https://%s.%s", domain, baseDomain)

	// Build WebSocket URL.
	wsScheme := "wss"
	serverURL, _ := url.Parse(c.server)
	if serverURL != nil && serverURL.Scheme == "http" {
		wsScheme = "ws"
	}
	wsHost := serverURL.Host
	wsURL := fmt.Sprintf("%s://%s/api/tunnel/connect?domain=%s", wsScheme, wsHost, url.QueryEscape(domain))
	if noAuth {
		wsURL += "&noauth=1"
	}

	wsHeader := http.Header{}
	wsHeader.Set("Authorization", "Bearer "+c.cfg.Token)

	authLabel := "on (ForwardAuth)"
	if noAuth {
		authLabel = "off (public)"
	}
	fmt.Printf("Tunnel:\n")
	fmt.Printf("  %s → 127.0.0.1:%s\n", publicURL, port)
	fmt.Printf("  Domain: %s\n", domain)
	fmt.Printf("  Auth:   %s\n", authLabel)
	fmt.Println("Press Ctrl+C to stop.")

	// Handle graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Reconnect loop.
	httpClient := &http.Client{Timeout: 60 * time.Second}
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		err := tunnelSession(wsURL, wsHeader, localTarget, httpClient)
		if ctx.Err() != nil {
			fmt.Println("\nTunnel stopped.")
			return nil
		}
		if err != nil {
			log.Printf("tunnel disconnected: %v", err)
		}
		fmt.Printf("Reconnecting in %s...\n", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			fmt.Println("\nTunnel stopped.")
			return nil
		}
		// Exponential backoff, capped.
		backoff = backoff * 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// tunnelSession runs a single WebSocket connection. It returns when the
// connection is lost or closed. The caller decides whether to reconnect.
func tunnelSession(wsURL string, header http.Header, localTarget string, httpClient *http.Client) error {
	const pongTimeout = 45 * time.Second

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   16 * 1024,
		WriteBufferSize:  16 * 1024,
	}
	ws, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ws.Close()

	writer := &wsMutexWriter{ws: ws}
	_ = writer // used below

	// Configure Ping handler — the server sends Pings every 30s; reset the
	// read deadline on each Ping and reply with a Pong.
	ws.SetReadDeadline(time.Now().Add(pongTimeout))
	ws.SetPingHandler(func(msg string) error {
		ws.SetReadDeadline(time.Now().Add(pongTimeout))
		return ws.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(10*time.Second))
	})

	fmt.Println("Connected.")

	// Read loop: read requests from server, proxy to local, send responses back.
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		var req tunnelMsg
		if err := json.Unmarshal(raw, &req); err != nil {
			log.Printf("bad tunnel message: %v", err)
			continue
		}
		if req.Type != "request" {
			continue
		}

		// Handle each request concurrently.
		go func(req tunnelMsg) {
			resp := proxyToLocal(httpClient, localTarget, req)
			data, _ := json.Marshal(resp)
			if err := writer.writeMessage(websocket.TextMessage, data); err != nil {
				log.Printf("ws write error: %v", err)
			}
		}(req)
	}
}

// proxyToLocal forwards a tunnel request to the local service and returns the response.
func proxyToLocal(httpClient *http.Client, localTarget string, req tunnelMsg) tunnelMsg {
	resp := tunnelMsg{Type: "response", ID: req.ID}

	// Decode request body.
	var bodyReader io.Reader
	if req.Body != "" {
		bodyBytes, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			resp.StatusCode = 502
			resp.Body = base64.StdEncoding.EncodeToString([]byte("bad request body encoding"))
			return resp
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	targetURL := localTarget + req.Path
	httpReq, err := http.NewRequest(req.Method, targetURL, bodyReader)
	if err != nil {
		resp.StatusCode = 502
		resp.Body = base64.StdEncoding.EncodeToString([]byte(err.Error()))
		return resp
	}

	// Copy headers from tunnel request, skip hop-by-hop headers.
	for k, vv := range req.Headers {
		kl := strings.ToLower(k)
		if kl == "host" || kl == "connection" || kl == "upgrade" || kl == "transfer-encoding" {
			continue
		}
		for _, v := range vv {
			httpReq.Header.Add(k, v)
		}
	}

	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		resp.StatusCode = 502
		resp.Body = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("local service error: %v", err)))
		return resp
	}
	defer httpResp.Body.Close()

	// Read response body (limit to 50MB).
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 50<<20))
	if err != nil {
		resp.StatusCode = 502
		resp.Body = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("read body: %v", err)))
		return resp
	}

	resp.StatusCode = httpResp.StatusCode
	resp.Headers = make(map[string][]string)
	for k, vv := range httpResp.Header {
		kl := strings.ToLower(k)
		if kl == "transfer-encoding" || kl == "connection" {
			continue
		}
		resp.Headers[k] = vv
	}
	resp.Body = base64.StdEncoding.EncodeToString(body)
	return resp
}
