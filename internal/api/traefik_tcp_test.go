package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hoveychen/muvee/internal/store"
)

// TCP router generation (migration 051). Like the other Traefik tests here the
// store is a concrete type that can't be mocked, so these drive the helpers the
// handler uses rather than the full HTTP handler.

func tcpDep(prefix string, port *int) *store.RunningDeploymentInfo {
	return &store.RunningDeploymentInfo{
		DeploymentID:    uuid.New(),
		ProjectID:       uuid.New(),
		DomainPrefix:    "app",
		HostIP:          "10.0.0.5",
		HostPort:        32768,
		TCPDomainPrefix: prefix,
		TCPHostPort:     port,
	}
}

func intp(v int) *int { return &v }

func TestAddTCPRouteEmitsSNIRouterAndService(t *testing.T) {
	s := &Server{baseDomain: "example.com"}
	cfg := traefikDynamicConfig{}

	if !s.addTCPRoute(&cfg, tcpDep("lkturn", intp(5349))) {
		t.Fatal("addTCPRoute reported no route for a fully configured project")
	}
	if cfg.TCP == nil {
		t.Fatal("tcp section not created")
	}
	r, ok := cfg.TCP.Routers["lkturn-tcp"]
	if !ok {
		t.Fatalf("router lkturn-tcp missing, got %v", cfg.TCP.Routers)
	}
	// Must match on SNI, not Host: a TCP router never sees an HTTP header.
	if r.Rule != "HostSNI(`lkturn.example.com`)" {
		t.Errorf("rule = %q", r.Rule)
	}
	// Shares 443 with the HTTP routers — that is the whole point, since the
	// motivating protocol (TURN) is only reachable on 443 in locked-down nets.
	if len(r.EntryPoints) != 1 || r.EntryPoints[0] != "websecure" {
		t.Errorf("entryPoints = %v, want [websecure]", r.EntryPoints)
	}
	// A TLS block on a TCP router means *terminate*, which is what lets the
	// backend speak plaintext and hold no cert.
	if r.TLS == nil || r.TLS.CertResolver != "letsencrypt" {
		t.Fatalf("tls = %+v, want letsencrypt termination", r.TLS)
	}
	if len(r.TLS.Domains) != 1 || r.TLS.Domains[0].Main != "lkturn.example.com" {
		t.Errorf("tls.domains = %+v", r.TLS.Domains)
	}

	svc, ok := cfg.TCP.Services["lkturn-tcp"]
	if !ok {
		t.Fatalf("service lkturn-tcp missing")
	}
	// TCP services address host:port and have no scheme — an "http://" here
	// would make Traefik reject the whole dynamic config.
	if len(svc.LoadBalancer.Servers) != 1 || svc.LoadBalancer.Servers[0].Address != "10.0.0.5:5349" {
		t.Fatalf("servers = %+v", svc.LoadBalancer.Servers)
	}
	// Routing must use the project's *own* published TCP port, never the
	// muvee-assigned HTTP host port.
	if strings.Contains(svc.LoadBalancer.Servers[0].Address, "32768") {
		t.Error("TCP service points at the HTTP host port")
	}
}

func TestAddTCPRouteMultiDomain(t *testing.T) {
	s := &Server{baseDomain: "muveeai.com", baseDomains: []string{"muveeai.com", "muvee.ai"}}
	cfg := traefikDynamicConfig{}
	s.addTCPRoute(&cfg, tcpDep("lkturn", intp(5349)))

	r := cfg.TCP.Routers["lkturn-tcp"]
	want := "(HostSNI(`lkturn.muveeai.com`) || HostSNI(`lkturn.muvee.ai`))"
	if r.Rule != want {
		t.Errorf("rule = %q, want %q", r.Rule, want)
	}
	// One HTTP-01 cert per base domain, same as the HTTP path — no wildcard.
	if len(r.TLS.Domains) != 2 {
		t.Errorf("tls.domains = %+v, want one per base domain", r.TLS.Domains)
	}
}

func TestAddTCPRouteSkippedWhenUnconfigured(t *testing.T) {
	s := &Server{baseDomain: "example.com"}
	for _, dep := range []*store.RunningDeploymentInfo{
		tcpDep("", intp(5349)), // no hostname
		tcpDep("lkturn", nil),  // no port
		tcpDep("lkturn", intp(0)),
	} {
		cfg := traefikDynamicConfig{}
		if s.addTCPRoute(&cfg, dep) {
			t.Errorf("half-configured project produced a route: %+v", dep)
		}
		if cfg.TCP != nil {
			t.Errorf("tcp section created for a project with no TCP route")
		}
	}
}

// The config every existing project sees must be byte-identical to before this
// feature existed — Traefik rejects the whole dynamic config on a malformed
// section, so an empty `"tcp":{}` would put every project at risk for a
// feature none of them use.
func TestTCPSectionOmittedWhenNobodyUsesIt(t *testing.T) {
	cfg := traefikDynamicConfig{
		HTTP: traefikHTTP{
			Routers:  map[string]traefikRouter{},
			Services: map[string]traefikService{},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "tcp") {
		t.Fatalf("tcp key present with no TCP routes: %s", b)
	}
}

func TestSNIMatchRule(t *testing.T) {
	if got := sniMatchRule([]string{"a.example.com"}); got != "HostSNI(`a.example.com`)" {
		t.Errorf("single host = %q, want bare HostSNI with no parens", got)
	}
	if got := sniMatchRule([]string{"a.x.com", "a.y.com"}); got != "(HostSNI(`a.x.com`) || HostSNI(`a.y.com`))" {
		t.Errorf("multi host = %q", got)
	}
}
