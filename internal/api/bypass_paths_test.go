package api

import (
	"strings"
	"testing"
)

func TestValidateBypassPaths(t *testing.T) {
	for _, bad := range []string{"/x`) || Host(`victim.example`", "health", "/a b", `/a\b`, "/a*/b"} {
		if validateBypassPaths(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if err := validateBypassPaths("/health\n/api/public/*\n\n/static/app.js"); err != nil {
		t.Errorf("legit paths rejected: %v", err)
	}
}

func TestAddBypassRoutersSkipsInjectedRows(t *testing.T) {
	cfg := traefikDynamicConfig{HTTP: &traefikHTTP{
		Routers:     map[string]traefikRouter{},
		Services:    map[string]traefikService{},
		Middlewares: map[string]traefikMiddleware{},
	}}
	addBypassRouters(&cfg, "p", "p", []string{"p.example.com"}, nil, "/ok\n/x`) || Host(`victim.example`")
	for name, r := range cfg.HTTP.Routers {
		if strings.Contains(r.Rule, "victim") {
			t.Fatalf("router %s carries injected rule %q", name, r.Rule)
		}
	}
	if len(cfg.HTTP.Routers) != 1 {
		t.Fatalf("want 1 bypass router, got %d", len(cfg.HTTP.Routers))
	}
}
