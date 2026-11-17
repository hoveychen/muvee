package embed_bridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// headerCapture records the request headers the downstream (the tenant's
// upstream) actually receives.
type headerCapture struct {
	cookies []string
	called  bool
}

func (h *headerCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.cookies = r.Header.Values("Cookie")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<html><head></head><body>ok</body></html>")
}

func newPluginWith(t *testing.T, next http.Handler, mutate func(*Config)) http.Handler {
	t.Helper()
	cfg := CreateConfig()
	if mutate != nil {
		mutate(cfg)
	}
	h, err := New(context.Background(), next, cfg, "embed-bridge")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func TestDefaultConfigStripsFwdSession(t *testing.T) {
	cfg := CreateConfig()
	want := []string{"muvee_fwd_session", "muvee_invite_token", "fwd_oauth_state"}
	if strings.Join(cfg.StripCookies, ",") != strings.Join(want, ",") {
		t.Fatalf("default StripCookies = %v, want %v", cfg.StripCookies, want)
	}
	if cfg.StripOnly {
		t.Fatal("default StripOnly must be false (the shared embed-bridge@file instance injects the SDK)")
	}
}

func TestStripsSessionCookieKeepsOthers(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", "a=1; muvee_fwd_session=secret.jwt; b=2")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(cap.cookies) != 1 || cap.cookies[0] != "a=1; b=2" {
		t.Fatalf("upstream Cookie = %q, want [\"a=1; b=2\"]", cap.cookies)
	}
}

func TestStripsAcrossMultipleCookieHeaders(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("Cookie", "muvee_fwd_session=x")
	req.Header.Add("Cookie", "theme=dark;muvee_fwd_session=y")
	h.ServeHTTP(httptest.NewRecorder(), req)
	got := strings.Join(cap.cookies, "|")
	if strings.Contains(got, "muvee_fwd_session") || got != "theme=dark" {
		t.Fatalf("upstream Cookie = %q, want [\"theme=dark\"]", cap.cookies)
	}
}

func TestOnlySessionCookieDeletesHeader(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", "muvee_fwd_session=secret")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(cap.cookies) != 0 {
		t.Fatalf("upstream Cookie = %q, want header removed", cap.cookies)
	}
}

func TestNoCookieHeaderUntouched(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !cap.called || len(cap.cookies) != 0 {
		t.Fatalf("called=%v cookies=%q", cap.called, cap.cookies)
	}
}

// Name match is exact: a tenant cookie that merely shares the prefix, or
// carries the name inside its value, must survive.
func TestStripsAllDefaultSecrets(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", "fwd_oauth_state=f1.x; app=1; muvee_invite_token=inv; muvee_fwd_session=s")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(cap.cookies) != 1 || cap.cookies[0] != "app=1" {
		t.Fatalf("upstream Cookie = %q, want [\"app=1\"]", cap.cookies)
	}
}

func TestStripIsExactNameMatch(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", "muvee_fwd_session_x=1; note=muvee_fwd_session=2; muvee_fwd_signed_out=1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	want := "muvee_fwd_session_x=1; note=muvee_fwd_session=2; muvee_fwd_signed_out=1"
	if len(cap.cookies) != 1 || cap.cookies[0] != want {
		t.Fatalf("upstream Cookie = %q, want %q", cap.cookies, want)
	}
}

// WebSocket upgrades bypass the HTML recorder; they must still be stripped.
func TestStripsOnUpgradeRequest(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, nil)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Cookie", "muvee_fwd_session=s; sid=9")
	h.ServeHTTP(&hijackableRecorder{httptest.NewRecorder()}, req)
	if len(cap.cookies) != 1 || cap.cookies[0] != "sid=9" {
		t.Fatalf("upstream Cookie = %q, want [\"sid=9\"]", cap.cookies)
	}
}

func TestEmptyStripListKeepsCookies(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, func(c *Config) { c.StripCookies = nil })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cookie", "muvee_fwd_session=s")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(cap.cookies) != 1 || cap.cookies[0] != "muvee_fwd_session=s" {
		t.Fatalf("upstream Cookie = %q", cap.cookies)
	}
}

// StripOnly instances (bypass routers) neither inject the SDK nor serve it.
func TestStripOnlyDoesNotInjectOrServeSDK(t *testing.T) {
	cap := &headerCapture{}
	h := newPluginWith(t, cap, func(c *Config) { c.StripOnly = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Cookie", "muvee_fwd_session=s; k=v")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "_embed-bridge.js") {
		t.Fatalf("StripOnly injected the SDK: %q", rec.Body.String())
	}
	if len(cap.cookies) != 1 || cap.cookies[0] != "k=v" {
		t.Fatalf("upstream Cookie = %q", cap.cookies)
	}

	cap.called = false
	req = httptest.NewRequest(http.MethodGet, "/_embed-bridge.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !cap.called {
		t.Fatal("StripOnly must forward /_embed-bridge.js to the upstream, not serve the SDK")
	}
}
