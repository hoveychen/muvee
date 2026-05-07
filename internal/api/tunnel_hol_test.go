package api

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestTunnelHOLBlocking reproduces the head-of-line blocking bug in the L4
// tunnel: a single slow stream stalls all other streams sharing the same
// tunnel WebSocket.
//
// Setup:
//   - One local TCP listener acts as the "service" the CLI dials on each
//     stream OPEN. The first accepted conn is HELD (not read, not written) to
//     simulate a slow/stuck consumer. Later conns echo a 200 OK back.
//   - One muvee tunnel server + the L4 client simulator (mirrors the real
//     CLI's read-loop behavior).
//
// Exercise:
//   - Stream A: a POST with a large body. The server multiplexes the body
//     onto the tunnel WS as DATA frames; the simulator's read loop calls
//     c.Write synchronously, which blocks once the stuck local conn's TCP
//     recv buffer fills.
//   - Stream B: a tiny GET issued AFTER A is wedged. With the bug present,
//     B's OPEN frame never gets processed because the simulator's read loop
//     is blocked on A's c.Write — B times out.
//
// Expected behavior (post-fix): A may stall indefinitely, but B completes
// promptly because each stream has its own writer goroutine and the tunnel
// read loop never blocks on a single stream's local conn.
func TestTunnelHOLBlocking(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	holdRelease := make(chan struct{})
	defer close(holdRelease)

	var connSeq atomic.Int32
	var heldConns []net.Conn
	var heldMu sync.Mutex

	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			seq := connSeq.Add(1)
			if seq == 1 {
				heldMu.Lock()
				heldConns = append(heldConns, c)
				heldMu.Unlock()
				go func(c net.Conn) {
					<-holdRelease
					c.Close()
				}(c)
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 65536)
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK"))
			}(c)
		}
	}()

	localAddr := listener.Addr().String()

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

	// Stream A: 4MB POST routed to the held conn. We don't care what happens
	// to A — only that it occupies enough in-flight buffer to wedge the read
	// loop. Use a generous timeout so the request goroutine doesn't tear down
	// while we're still asserting on stream B.
	bigBody := make([]byte, 4*1024*1024)
	streamADone := make(chan struct{})
	go func() {
		defer close(streamADone)
		req, _ := http.NewRequest("POST", ts.URL+"/big", bytes.NewReader(bigBody))
		req.Host = "t-test-fox.example.com"
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err == nil && resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()

	// Give A time to push data through and saturate the stuck local conn's
	// TCP recv buffer + the tunnel's WS buffers.
	time.Sleep(2 * time.Second)

	// Stream B: tiny GET. Should round-trip in milliseconds. With the HOL
	// bug, the CLI read loop is stuck on A's c.Write and B never makes it
	// past the OPEN frame.
	type result struct {
		body string
		err  error
	}
	streamB := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest("GET", ts.URL+"/small", nil)
		req.Host = "t-test-fox.example.com"
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			streamB <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		streamB <- result{body: string(body)}
	}()

	select {
	case r := <-streamB:
		if r.err != nil {
			t.Fatalf("stream B failed (HOL bug): %v", r.err)
		}
		if r.body != "OK" {
			t.Fatalf("stream B got unexpected body: %q", r.body)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("stream B timed out — head-of-line blocking is preventing parallel streams")
	}
}
