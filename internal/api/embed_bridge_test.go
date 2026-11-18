package api

import (
	"reflect"
	"testing"
)

func newTestTraefikConfig() *traefikDynamicConfig {
	return &traefikDynamicConfig{HTTP: &traefikHTTP{
		Routers:  map[string]traefikRouter{},
		Services: map[string]traefikService{},
	}}
}

// attachEmbedBridge is the helper applied to every project / tunnel /
// domain-only router emitted from handleTraefikConfig. The contract:
//   - appends embedBridgeMiddlewareName to router.Middlewares
//   - keeps any pre-existing middlewares (forwardAuth) ahead of it
//   - is idempotent: calling twice does not double-append
//   - declares the instance in the same document (BACKLOG-20261117-007: no
//     longer the host-mounted embed-bridge@file)
func TestAttachEmbedBridge_AppendToEmptyChain(t *testing.T) {
	cfg := newTestTraefikConfig()
	r := traefikRouter{}
	attachEmbedBridge(cfg, &r)
	if got := r.Middlewares; len(got) != 1 || got[0] != embedBridgeMiddlewareName {
		t.Errorf("got %v want [%s]", got, embedBridgeMiddlewareName)
	}
}

func TestAttachEmbedBridge_AppendsAfterExisting(t *testing.T) {
	cfg := newTestTraefikConfig()
	r := traefikRouter{Middlewares: []string{"myapp-auth"}}
	attachEmbedBridge(cfg, &r)
	if got := r.Middlewares; len(got) != 2 || got[0] != "myapp-auth" || got[1] != embedBridgeMiddlewareName {
		t.Errorf("expected forwardAuth then embed-bridge; got %v", got)
	}
}

func TestAttachEmbedBridge_Idempotent(t *testing.T) {
	cfg := newTestTraefikConfig()
	r := traefikRouter{}
	attachEmbedBridge(cfg, &r)
	attachEmbedBridge(cfg, &r)
	attachEmbedBridge(cfg, &r)
	if got := r.Middlewares; len(got) != 1 || got[0] != embedBridgeMiddlewareName {
		t.Errorf("idempotency broken: got %v", got)
	}
}

// The instance must carry an explicit strip list naming every authservice
// cookie, new and legacy, instead of relying on the plugin's built-in default
// (which only knows the legacy names).
func TestAttachEmbedBridge_DeclaresExplicitStripList(t *testing.T) {
	cfg := newTestTraefikConfig()
	r := traefikRouter{}
	attachEmbedBridge(cfg, &r)
	mw, ok := cfg.HTTP.Middlewares[embedBridgeMiddlewareName]
	if !ok || mw.Plugin == nil {
		t.Fatalf("embed-bridge instance not declared: %+v", cfg.HTTP.Middlewares)
	}
	conf := mw.Plugin["embed-bridge"]
	if conf["scriptPath"] != "/_embed-bridge.js" {
		t.Errorf("scriptPath = %v", conf["scriptPath"])
	}
	if !reflect.DeepEqual(conf["stripCookies"], FwdAuthCookieNames) {
		t.Errorf("stripCookies = %v, want %v", conf["stripCookies"], FwdAuthCookieNames)
	}
	if _, set := conf["stripOnly"]; set {
		t.Errorf("project instance must not be strip-only: %v", conf)
	}
}

// The strip list covers the host-only session/SSO/state cookies, the dev
// fallbacks and every pre-isolation base-domain name.
func TestFwdAuthCookieNamesCoverage(t *testing.T) {
	want := []string{
		"__Host-muvee_fwd", "__Host-muvee_sso", "__Host-fwd_state", "__Host-fwd_apex_state",
		"__Host-muvee_fwd_signed_out", "__Host-fwd_oauth_redirect",
		"muvee_fwd", "muvee_sso", "fwd_state", "fwd_apex_state",
		"muvee_fwd_session", "fwd_oauth_state", "fwd_oauth_host", "muvee_invite_token",
		"muvee_fwd_signed_out", "fwd_oauth_redirect",
		"__Host-fwd_login_hint", "fwd_login_hint",
		"__Host-fwd_state_*", "fwd_state_*", "__Host-fwd_apex_state_*", "fwd_apex_state_*",
	}
	have := map[string]bool{}
	for _, n := range FwdAuthCookieNames {
		if have[n] {
			t.Errorf("duplicate cookie name %q", n)
		}
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("FwdAuthCookieNames is missing %q", n)
		}
	}
	if len(have) != len(want) {
		t.Errorf("FwdAuthCookieNames has %d names, want %d: %v", len(have), len(want), FwdAuthCookieNames)
	}
}

func TestStripFwdSessionMiddlewareExplicitList(t *testing.T) {
	cfg := newTestTraefikConfig()
	ensureStripFwdSessionMiddleware(cfg)
	conf := cfg.HTTP.Middlewares[stripFwdSessionMiddlewareName].Plugin["embed-bridge"]
	if conf["stripOnly"] != true {
		t.Errorf("stripOnly = %v, want true", conf["stripOnly"])
	}
	if !reflect.DeepEqual(conf["stripCookies"], FwdAuthCookieNames) {
		t.Errorf("stripCookies = %v, want %v", conf["stripCookies"], FwdAuthCookieNames)
	}
}

// ForwardAuth copies the renewed session cookie (new, dev and legacy names)
// from a 2xx /verify onto the proxied response.
func TestProjectForwardAuthResponseCookies(t *testing.T) {
	fa := projectForwardAuth("http://authservice:4181/verify?project_id=x")
	want := []string{"__Host-muvee_fwd", "muvee_fwd", "muvee_fwd_session"}
	if !reflect.DeepEqual(fa.AddAuthCookiesToResponse, want) {
		t.Errorf("AddAuthCookiesToResponse = %v, want %v", fa.AddAuthCookiesToResponse, want)
	}
}
