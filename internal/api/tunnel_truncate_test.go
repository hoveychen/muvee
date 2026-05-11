package api

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
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

// TestTunnelResponseTruncationRace reproduces a body-truncation race in
// handleTunnelTraffic's tunnel→browser loop.
//
// The CLI writes frameData(response) immediately followed by frameClose to the
// WebSocket. On the server, serveConn pushes the data into stream.inbox and
// then closes stream.closed. The handler's select has both cases ready
// simultaneously and Go picks one at random: when it picks <-stream.closed,
// it returns without draining the in-flight frameData, the hijacked conn is
// closed mid-body, and Traefik (upstream of muvee-server) returns 500 for
// "unexpected EOF during body copy".
//
// Sequential bursts against an immediately-closing backend trigger this with
// observed real-world frequency ~20%.
func TestTunnelResponseTruncationRace(t *testing.T) {
	// Local backend that speaks raw HTTP/1.1 and closes the conn the instant
	// the response is flushed — the tightest possible coupling between the
	// CLI's frameData and frameClose writes.
	const responseBodySize = 64 * 1024 // > one 16K frame so the response must be chunked
	bodyBuf := make([]byte, responseBodySize)
	if _, err := rand.Read(bodyBuf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	wantBody := hex.EncodeToString(bodyBuf) // ASCII so comparison errors are readable

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	localAddr := listener.Addr().String()

	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				// Drain the request headers; we don't care about contents.
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" || line == "\n" {
						break
					}
				}
				// Mirror Python BaseHTTPServer / typical real backends: emit
				// the response in several separate writes (status, headers,
				// body) so the CLI's c.Read returns it in several chunks,
				// producing several frameData frames followed by frameClose.
				// One-frameData responses don't race; multi-frameData ones do.
				headers := fmt.Sprintf(
					"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
					len(wantBody),
				)
				_, _ = c.Write([]byte(headers))
				// Tiny pause so the headers and body show up as distinct
				// TCP segments → distinct CLI reads → distinct frameData.
				time.Sleep(500 * time.Microsecond)
				_, _ = c.Write([]byte(wantBody))
				// Close (via defer) fires immediately after Write returns.
			}(c)
		}
	}()

	s := &Server{baseDomain: "example.com", tunnels: newTunnelRegistry()}

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

	// Use a fresh transport that does NOT pool conns — every request walks
	// the full hijack path, which is where the race lives.
	tr := &http.Transport{
		DisableKeepAlives: true,
		MaxIdleConns:      0,
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

	const (
		iterations  = 200
		concurrency = 8
	)
	var (
		mu        sync.Mutex
		truncated int
		wrongCode int
		firstFail string
	)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			req, _ := http.NewRequest("GET", ts.URL+"/probe", nil)
			req.Host = "t-test-fox.example.com"
			resp, err := client.Do(req)
			if err != nil {
				mu.Lock()
				truncated++
				if firstFail == "" {
					firstFail = fmt.Sprintf("iter %d: do err: %v", i, err)
				}
				mu.Unlock()
				return
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				mu.Lock()
				wrongCode++
				if firstFail == "" {
					firstFail = fmt.Sprintf("iter %d: status=%d body=%q", i, resp.StatusCode, string(body))
				}
				mu.Unlock()
				return
			}
			if readErr != nil || string(body) != wantBody {
				mu.Lock()
				truncated++
				if firstFail == "" {
					firstFail = fmt.Sprintf("iter %d: body_len=%d want_len=%d readErr=%v",
						i, len(body), len(wantBody), readErr)
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if truncated+wrongCode > 0 {
		t.Fatalf("tunnel truncated responses: %d/%d truncated, %d/%d wrong status. first=%s",
			truncated, iterations, wrongCode, iterations, firstFail)
	}
}
