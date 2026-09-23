package api

import "testing"

func cfTunnelTestServer() *Server {
	return &Server{
		baseDomain:      "muveeai.com",
		baseDomains:     []string{"muveeai.com", "cfdemo.com"},
		cfTunnelDomains: []string{"cfdemo.com"},
		authServiceURL:  "http://muvee-authservice:4181",
	}
}

// A project prefix served under an ACME base and a tunnel base must split into
// the ACME router (websecure, one cert per ACME host) and a `_cf` sibling on
// cftunnel with no TLS — including the path-scoped bypass/device-flow routers.
func TestApplyCFTunnelDomainsSplitsRouters(t *testing.T) {
	s := cfTunnelTestServer()
	hosts := s.hostsForPrefix("foo")
	cfg := traefikDynamicConfig{HTTP: &traefikHTTP{
		Routers:  map[string]traefikRouter{},
		Services: map[string]traefikService{},
	}}
	tls := &traefikTLS{CertResolver: "letsencrypt", Domains: tlsDomainsFor(hosts)}
	cfg.HTTP.Routers["foo"] = traefikRouter{
		Rule:        hostMatchRule(hosts),
		EntryPoints: []string{"websecure"},
		Service:     "foo",
		TLS:         tls,
		Middlewares: []string{"foo-auth", embedBridgeMiddlewareRef},
	}
	addDeviceFlowRouter(&cfg, "foo", hosts, tls)

	s.applyCFTunnelDomains(&cfg)

	acme := cfg.HTTP.Routers["foo"]
	if acme.Rule != "Host(`foo.muveeai.com`)" {
		t.Errorf("ACME router rule = %q", acme.Rule)
	}
	if acme.TLS == nil || len(acme.TLS.Domains) != 1 || acme.TLS.Domains[0].Main != "foo.muveeai.com" {
		t.Errorf("ACME router TLS = %+v, want one cert for foo.muveeai.com", acme.TLS)
	}
	if len(tls.Domains) != 2 {
		t.Errorf("shared TLS block was mutated in place: %+v", tls.Domains)
	}

	cf, ok := cfg.HTTP.Routers["foo_cf"]
	if !ok {
		t.Fatal("tunnel sibling foo_cf not emitted")
	}
	if cf.Rule != "Host(`foo.cfdemo.com`)" || cf.TLS != nil {
		t.Errorf("tunnel router = rule %q TLS %+v, want Host(`foo.cfdemo.com`) without TLS", cf.Rule, cf.TLS)
	}
	if len(cf.EntryPoints) != 1 || cf.EntryPoints[0] != "cftunnel" {
		t.Errorf("tunnel router entrypoints = %v, want [cftunnel]", cf.EntryPoints)
	}
	if len(cf.Middlewares) != 2 || cf.Middlewares[0] != "foo-auth" {
		t.Errorf("tunnel router lost the auth chain: %v", cf.Middlewares)
	}

	df := cfg.HTTP.Routers["foo-device-flow_cf"]
	if df.Rule != "Host(`foo.cfdemo.com`) && PathPrefix(`/_oauth`)" || df.Priority != 200 {
		t.Errorf("device-flow sibling = %+v", df)
	}
	if got := cfg.HTTP.Routers["foo-device-flow"].Rule; got != "Host(`foo.muveeai.com`) && PathPrefix(`/_oauth`)" {
		t.Errorf("ACME device-flow rule = %q", got)
	}
}

// With CF_TUNNEL_DOMAINS unset the config must be byte-identical to before.
func TestApplyCFTunnelDomainsNoop(t *testing.T) {
	s := &Server{baseDomain: "muveeai.com", baseDomains: []string{"muveeai.com", "muvee.ai"}}
	hosts := s.hostsForPrefix("foo")
	cfg := traefikDynamicConfig{HTTP: &traefikHTTP{
		Routers: map[string]traefikRouter{"foo": {
			Rule:        hostMatchRule(hosts),
			EntryPoints: []string{"websecure"},
			TLS:         &traefikTLS{CertResolver: "letsencrypt", Domains: tlsDomainsFor(hosts)},
		}},
		Services: map[string]traefikService{},
	}}
	s.applyCFTunnelDomains(&cfg)
	if len(cfg.HTTP.Routers) != 1 || cfg.HTTP.Routers["foo"].Rule != hostMatchRule(hosts) {
		t.Errorf("routers changed with no tunnel domains: %+v", cfg.HTTP.Routers)
	}
}

// A router whose hosts are all tunnel hosts moves wholesale; a TCP route drops
// its tunnel hosts (SNI passthrough doesn't survive the tunnel).
func TestApplyCFTunnelDomainsTunnelOnlyAndTCP(t *testing.T) {
	s := cfTunnelTestServer()
	cfg := traefikDynamicConfig{
		HTTP: &traefikHTTP{
			Routers: map[string]traefikRouter{"alias": {
				Rule:        "Host(`shop.cfdemo.com`)",
				EntryPoints: []string{"websecure"},
				TLS:         &traefikTLS{CertResolver: "letsencrypt"},
			}},
			Services: map[string]traefikService{},
		},
	}
	s.addTCPRoute(&cfg, tcpDep("lkturn", intp(5349)))
	s.applyCFTunnelDomains(&cfg)

	if _, ok := cfg.HTTP.Routers["alias"]; ok {
		t.Error("tunnel-only router kept its ACME original")
	}
	if r := cfg.HTTP.Routers["alias_cf"]; r.Rule != "Host(`shop.cfdemo.com`)" || r.TLS != nil {
		t.Errorf("alias_cf = %+v", r)
	}

	tcp := cfg.TCP.Routers["lkturn-tcp"]
	if tcp.Rule != "HostSNI(`lkturn.muveeai.com`)" {
		t.Errorf("TCP rule = %q, want only the ACME host", tcp.Rule)
	}
	if len(tcp.TLS.Domains) != 1 {
		t.Errorf("TCP TLS domains = %+v", tcp.TLS.Domains)
	}
}

func TestApplyCFTunnelDomainsPanelRouters(t *testing.T) {
	s := cfTunnelTestServer()
	cfg := traefikDynamicConfig{}
	s.applyCFTunnelDomains(&cfg)

	panel := cfg.HTTP.Routers["panel_cf"]
	if panel.Rule != "(Host(`cfdemo.com`) || Host(`app.cfdemo.com`))" || panel.Service != "muvee@docker" {
		t.Errorf("panel_cf = %+v", panel)
	}
	oauth := cfg.HTTP.Routers["panel-oauth_cf"]
	if oauth.Service != deviceFlowServiceName || oauth.Priority != 200 {
		t.Errorf("panel-oauth_cf = %+v", oauth)
	}
	if svc := cfg.HTTP.Services[deviceFlowServiceName]; len(svc.LoadBalancer.Servers) != 1 {
		t.Errorf("authservice backend not declared: %+v", svc)
	}
}

func TestIsCFTunnelHost(t *testing.T) {
	s := cfTunnelTestServer()
	for host, want := range map[string]bool{
		"cfdemo.com":         true,
		"foo.cfdemo.com":     true,
		"APP.cfdemo.com:443": true,
		"foo.muveeai.com":    false,
		"notcfdemo.com":      false,
		"shop.example.org":   false,
	} {
		if got := s.isCFTunnelHost(host); got != want {
			t.Errorf("isCFTunnelHost(%q) = %v, want %v", host, got, want)
		}
	}
}
