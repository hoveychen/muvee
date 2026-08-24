package embed_bridge

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// hijackableRecorder is a minimal ResponseWriter that ALSO implements
// http.Hijacker. Used to assert that the embed-bridge plugin does not
// strip the Hijacker capability for HTTP Upgrade requests (WebSocket etc.) —
// without this, Traefik returns "can't switch protocols using non-Hijacker
// ResponseWriter type" and the WS handshake dies as 500.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}

// fakeNext is the downstream handler the middleware wraps. Each test
// configures content-type / status / body it should emit.
type fakeNext struct {
	contentType string
	statusCode  int
	body        string
	extraHdr    map[string]string
}

func (f *fakeNext) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if f.contentType != "" {
		w.Header().Set("Content-Type", f.contentType)
	}
	for k, v := range f.extraHdr {
		w.Header().Set(k, v)
	}
	if f.statusCode != 0 {
		w.WriteHeader(f.statusCode)
	}
	io.WriteString(w, f.body)
}

func newPlugin(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	cfg := CreateConfig()
	h, err := New(context.Background(), next, cfg, "embed-bridge")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func TestServesSDKAtScriptPath(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_embed-bridge.js", nil)
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("downstream handler must not be called for /_embed-bridge.js")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type: got %q", ct)
	}
	body := rec.Body.String()
	// One smoke marker per SDK component so a missing concat (e.g. the v2
	// const dropped, html2canvas const renamed) trips this test rather than
	// surfacing only at runtime in the iframe.
	wantMarkers := []string{
		"embed:meta",                 // v0 meta upstream
		"embed:selection",            // v1 selection upstream (also matches v2)
		"embed:selection-screenshot", // v2 selection-screenshot upstream
		"html2canvas",                // v2 dependency: bundled UMD must be present
		"postMessage",                // generic shape sanity
	}
	for _, marker := range wantMarkers {
		if !strings.Contains(body, marker) {
			t.Errorf("body missing SDK marker %q (body length=%d)", marker, len(body))
		}
	}
}

func TestInjectsScriptIntoHTML(t *testing.T) {
	next := &fakeNext{
		contentType: "text/html; charset=utf-8",
		statusCode:  http.StatusOK,
		body:        `<!doctype html><html><head><title>Hi</title></head><body>x</body></html>`,
	}
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `<script src="/_embed-bridge.js" defer></script></head>`) {
		t.Errorf("expected script splice before </head>, got: %s", body)
	}
	// Original markup preserved.
	if !strings.Contains(body, `<title>Hi</title>`) {
		t.Errorf("title removed: %s", body)
	}
	// Content-Length matches new body.
	gotLen := rec.Header().Get("Content-Length")
	if gotLen == "" {
		t.Errorf("missing Content-Length header")
	}
}

func TestPassesThroughNonHTML(t *testing.T) {
	next := &fakeNext{
		contentType: "application/json",
		statusCode:  http.StatusOK,
		body:        `{"ok":true}`,
	}
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/data", nil))

	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Errorf("body altered for non-HTML: %q", got)
	}
}

func TestPassesThroughHTMLWithoutHead(t *testing.T) {
	// Some fragment / partial HTML responses (htmx etc.) may have no </head>.
	// Splice should be a no-op rather than corrupt the body.
	next := &fakeNext{
		contentType: "text/html",
		statusCode:  http.StatusOK,
		body:        `<div>partial</div>`,
	}
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fragment", nil))

	if got := rec.Body.String(); got != `<div>partial</div>` {
		t.Errorf("partial HTML mutated: %q", got)
	}
}

func TestPassesThrough3xxRedirects(t *testing.T) {
	next := &fakeNext{
		contentType: "text/html",
		statusCode:  http.StatusFound,
		body:        "", // typical for 302
		extraHdr:    map[string]string{"Location": "/login"},
	}
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusFound {
		t.Errorf("status: got %d want 302", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Location header lost: %v", rec.Header())
	}
}

func TestDropsETagAfterSplice(t *testing.T) {
	next := &fakeNext{
		contentType: "text/html",
		statusCode:  http.StatusOK,
		body:        `<html><head></head><body></body></html>`,
		extraHdr:    map[string]string{"ETag": `"abc123"`, "Last-Modified": "Mon, 01 Jan 2024 00:00:00 GMT"},
	}
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Header().Get("ETag") != "" {
		t.Errorf("ETag must be dropped after body mutation; got %q", rec.Header().Get("ETag"))
	}
	if rec.Header().Get("Last-Modified") != "" {
		t.Errorf("Last-Modified must be dropped; got %q", rec.Header().Get("Last-Modified"))
	}
}

func TestCustomScriptPath(t *testing.T) {
	cfg := CreateConfig()
	cfg.ScriptPath = "/__bridge.js"
	next := &fakeNext{contentType: "text/html", statusCode: 200, body: `<html><head></head></html>`}
	h, _ := New(context.Background(), next, cfg, "embed-bridge")

	// (a) intercepts the custom path
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/__bridge.js", nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/javascript") {
		t.Errorf("custom path not intercepted: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}

	// (b) injects the custom path into the script tag
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec2.Body.String(), `src="/__bridge.js"`) {
		t.Errorf("custom path not used in injected tag: %s", rec2.Body.String())
	}
}

// TestUpgradeRequestPreservesHijacker is the regression for the cws.muveeai.com
// WebSocket "500 Internal Server Error" symptom: the plugin wrapped every
// downstream call in a buffering responseRecorder that did NOT implement
// http.Hijacker, so when Traefik's reverse-proxy tried to switch protocols on
// a 101 it failed with "can't switch protocols using non-Hijacker
// ResponseWriter type". For Upgrade requests (websocket / h2c / etc.) the
// plugin must pass through to next with the original ResponseWriter so the
// Hijacker capability survives.
func TestUpgradeRequestPreservesHijacker(t *testing.T) {
	var receivedRW http.ResponseWriter
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		receivedRW = w
	})
	h := newPlugin(t, next)

	rw := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	h.ServeHTTP(rw, req)

	if receivedRW == nil {
		t.Fatal("downstream handler never invoked")
	}
	if _, ok := receivedRW.(http.Hijacker); !ok {
		t.Fatalf("downstream got non-Hijacker ResponseWriter %T; Upgrade requests must pass-through so Traefik can hijack the client conn", receivedRW)
	}
}

func TestPOSTToScriptPathFallsThrough(t *testing.T) {
	// Only GET is intercepted — a POST to /_embed-bridge.js (unlikely but
	// possible) should reach downstream.
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	h := newPlugin(t, next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_embed-bridge.js", nil))
	if !called {
		t.Errorf("POST to script path must reach downstream")
	}
}

// streamProbe is a ResponseWriter that reports the moment the first body byte
// reaches it, so a test can distinguish "streamed" from "buffered until the
// downstream handler returns" without racing on an httptest.ResponseRecorder.
type streamProbe struct {
	hdr     http.Header
	mu      sync.Mutex
	body    strings.Builder
	status  int
	flushes int
	written chan struct{}
	once    sync.Once
}

func newStreamProbe() *streamProbe {
	return &streamProbe{hdr: make(http.Header), written: make(chan struct{})}
}

func (s *streamProbe) Header() http.Header { return s.hdr }

func (s *streamProbe) WriteHeader(code int) {
	s.mu.Lock()
	s.status = code
	s.mu.Unlock()
}

func (s *streamProbe) Write(b []byte) (int, error) {
	s.mu.Lock()
	n, err := s.body.Write(b)
	s.mu.Unlock()
	s.once.Do(func() { close(s.written) })
	return n, err
}

func (s *streamProbe) Flush() {
	s.mu.Lock()
	s.flushes++
	s.mu.Unlock()
}

func (s *streamProbe) flushCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushes
}

func (s *streamProbe) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body.String()
}

func (s *streamProbe) statusCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// TestStreamingResponseIsNotBuffered is the regression for the fleet-cloud
// "SSE returns 0 bytes through the muvee edge" symptom. The plugin only
// bypassed its buffering responseRecorder when the *request* carried an
// Upgrade header. SSE (and any other long-lived stream) carries no such
// header, so the recorder soaked up the whole response and only flushed once
// next.ServeHTTP returned — which for an endless stream is never. Verified
// against fleet-cloud.muveeai.com/events: 0 bytes in 8s through the edge,
// immediate ": connected" from inside the container.
//
// A non-HTML response must therefore reach the client while the downstream
// handler is still running, and must keep its http.Flusher.
func TestStreamingResponseIsNotBuffered(t *testing.T) {
	release := make(chan struct{})
	sawFlusher := make(chan bool, 1)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ": connected\n\n")
		f, ok := w.(http.Flusher)
		sawFlusher <- ok
		if ok {
			f.Flush()
		}
		<-release // long-lived stream: the handler has NOT returned yet
	})

	h := newPlugin(t, next)
	probe := newStreamProbe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(probe, httptest.NewRequest(http.MethodGet, "/events", nil))
	}()

	select {
	case <-probe.written:
	case <-time.After(2 * time.Second):
		close(release)
		<-done
		t.Fatal("no body byte reached the client while the stream was open: " +
			"the response is being buffered, which breaks SSE")
	}

	if !<-sawFlusher {
		t.Error("downstream lost http.Flusher; a streaming handler cannot push")
	}
	if got := probe.statusCode(); got != http.StatusOK {
		t.Errorf("status: got %d want 200", got)
	}
	if got := probe.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type not forwarded to client: got %q", got)
	}
	if got := probe.String(); !strings.Contains(got, ": connected") {
		t.Errorf("streamed body missing: %q", got)
	}

	close(release)
	<-done
}

// TestStreamingFlushesWithoutDownstreamFlush is the second half of the SSE
// regression, and the one a compiled-Go unit test alone would have missed.
//
// This plugin is interpreted by yaegi inside Traefik. When the recorder is
// handed to the compiled downstream handler, yaegi wraps it in a shim that
// exposes only Header/Write/WriteHeader — the plugin's own Flush method is NOT
// visible across that boundary. So Traefik's reverse proxy, which drives
// streaming by type-asserting http.Flusher on the ResponseWriter it was given,
// never finds one and never flushes. The bytes then sit in net/http's 4 KB
// response buffer and an SSE client sees nothing.
//
// Verified against a real traefik:v3.6.1 + yaegi harness: forwarding writes
// without self-flushing still produced 0 bytes for /events.
//
// The middleware must therefore flush the real ResponseWriter itself on every
// passthrough write, rather than waiting to be asked.
func TestStreamingFlushesWithoutDownstreamFlush(t *testing.T) {
	release := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Deliberately never calls Flush — mirrors the yaegi boundary where
		// the downstream cannot see the plugin's Flusher.
		io.WriteString(w, ": connected\n\n")
		<-release
	})

	h := newPlugin(t, next)
	probe := newStreamProbe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(probe, httptest.NewRequest(http.MethodGet, "/events", nil))
	}()

	select {
	case <-probe.written:
	case <-time.After(2 * time.Second):
		close(release)
		<-done
		t.Fatal("nothing reached the client while the stream was open")
	}

	// Give the middleware a moment to have issued its flush alongside the write.
	deadline := time.Now().Add(2 * time.Second)
	for probe.flushCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if probe.flushCount() == 0 {
		close(release)
		<-done
		t.Fatal("middleware forwarded the write but never flushed the real " +
			"ResponseWriter; under yaegi the chunk stalls in net/http's buffer")
	}

	close(release)
	<-done
}

// TestBufferingRecorderPreservesHijacker covers a handler that hijacks before
// writing anything: the recorder is still in place at that point (the
// content-type is unknown until WriteHeader), so it must forward Hijack to
// the real ResponseWriter rather than swallow the capability.
func TestBufferingRecorderPreservesHijacker(t *testing.T) {
	var gotHijacker bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, gotHijacker = w.(http.Hijacker)
	})
	h := newPlugin(t, next)

	rw := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))

	if !gotHijacker {
		t.Error("downstream lost http.Hijacker on a non-Upgrade request")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
