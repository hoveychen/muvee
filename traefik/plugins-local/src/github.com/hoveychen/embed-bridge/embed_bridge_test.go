package embed_bridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	if !strings.Contains(body, "embed:meta") || !strings.Contains(body, "postMessage") {
		t.Errorf("body missing expected SDK markers: first 80 = %q", body[:min(80, len(body))])
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
