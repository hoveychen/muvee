package api

import (
	"regexp"
	"strings"

	"github.com/hoveychen/muvee/internal/domains"
)

// Cloudflare Tunnel base domains (CF_TUNNEL_DOMAINS).
//
// Hosts under such a base never reach Traefik's public :80/:443. cloudflared
// forwards them from Cloudflare's edge — which owns the TLS cert — to the
// internal plain-HTTP `cftunnel` entrypoint (traefik/traefik.yml). Traefik
// must therefore neither request ACME certs for them (HTTP-01 can't complete
// through the tunnel) nor expect TLS on that entrypoint.
//
// Rather than threading this through every router builder in
// handleTraefikConfig, the config is generated exactly as for ACME domains and
// then post-processed by applyCFTunnelDomains: each router whose hosts include
// tunnel hosts is split into the original (ACME hosts only) and a sibling on
// cftunnel with no TLS.

const (
	cfTunnelEntryPoint = "cftunnel"
	// cfTunnelRouterSuffix names the sibling router. `_` cannot appear in a
	// domain prefix (validDomainPrefix), so the sibling can't collide with a
	// project's own router name.
	cfTunnelRouterSuffix = "_cf"
	// cfTunnelPanelService is the control-plane panel service that
	// docker-compose declares via labels on muvee-server.
	cfTunnelPanelService = "muvee@docker"
)

var hostTokenRe = regexp.MustCompile("Host(?:SNI)?\\(`([^`]+)`\\)")

// isCFTunnelHost reports whether host belongs to one of the tunnel bases.
func (s *Server) isCFTunnelHost(host string) bool {
	if len(s.cfTunnelDomains) == 0 {
		return false
	}
	base, ok := domains.Match(host, s.baseDomains)
	if !ok {
		return false
	}
	for _, d := range s.cfTunnelDomains {
		if d == base {
			return true
		}
	}
	return false
}

// splitHosts partitions hosts into ACME-served and tunnel-served, keeping order.
func (s *Server) splitHosts(hosts []string) (acme, tunnel []string) {
	for _, h := range hosts {
		if s.isCFTunnelHost(h) {
			tunnel = append(tunnel, h)
		} else {
			acme = append(acme, h)
		}
	}
	return acme, tunnel
}

// splitRule separates a generated rule into its host list and the remainder.
// Every router handleTraefikConfig emits starts with hostMatchRule(hosts) (or
// sniMatchRule for TCP), optionally followed by ` && <path rule>`; ok is false
// for a rule of any other shape, which is then left untouched.
func splitRule(rule string, sni bool) (hosts []string, rest string, ok bool) {
	for _, m := range hostTokenRe.FindAllStringSubmatch(rule, -1) {
		hosts = append(hosts, m[1])
	}
	if len(hosts) == 0 {
		return nil, "", false
	}
	build := hostMatchRule
	if sni {
		build = sniMatchRule
	}
	prefix := build(hosts)
	if !strings.HasPrefix(rule, prefix) {
		return nil, "", false
	}
	return hosts, strings.TrimPrefix(rule, prefix), true
}

// applyCFTunnelDomains rewrites cfg so hosts under CF_TUNNEL_DOMAINS are served
// on the cftunnel entrypoint without TLS, and adds the panel and /_oauth
// routers for those bases. A no-op when CF_TUNNEL_DOMAINS is empty.
func (s *Server) applyCFTunnelDomains(cfg *traefikDynamicConfig) {
	if len(s.cfTunnelDomains) == 0 {
		return
	}
	if cfg.HTTP != nil {
		// Snapshot the names: siblings are inserted while iterating, and a
		// ranged map may or may not yield them — a visited sibling (all tunnel
		// hosts) would be split again into `_cf_cf` and itself deleted.
		names := make([]string, 0, len(cfg.HTTP.Routers))
		for name := range cfg.HTTP.Routers {
			names = append(names, name)
		}
		for _, name := range names {
			r := cfg.HTTP.Routers[name]
			hosts, rest, ok := splitRule(r.Rule, false)
			if !ok {
				continue
			}
			acme, tunnel := s.splitHosts(hosts)
			if len(tunnel) == 0 {
				continue
			}
			sibling := r
			sibling.Rule = hostMatchRule(tunnel) + rest
			sibling.EntryPoints = []string{cfTunnelEntryPoint}
			sibling.TLS = nil
			sibling.Middlewares = append([]string(nil), r.Middlewares...)
			cfg.HTTP.Routers[name+cfTunnelRouterSuffix] = sibling

			if len(acme) == 0 {
				delete(cfg.HTTP.Routers, name)
				continue
			}
			r.Rule = hostMatchRule(acme) + rest
			if r.TLS != nil {
				tls := *r.TLS
				tls.Domains = tlsDomainsFor(acme)
				r.TLS = &tls
			}
			cfg.HTTP.Routers[name] = r
		}
	}

	// TCP routes rely on SNI passthrough on :443, which a Cloudflare Tunnel
	// doesn't carry — tunnel hosts are simply dropped from them.
	if cfg.TCP != nil {
		for name, r := range cfg.TCP.Routers {
			hosts, rest, ok := splitRule(r.Rule, true)
			if !ok {
				continue
			}
			acme, tunnel := s.splitHosts(hosts)
			if len(tunnel) == 0 {
				continue
			}
			if len(acme) == 0 {
				delete(cfg.TCP.Routers, name)
				delete(cfg.TCP.Services, r.Service)
				continue
			}
			r.Rule = sniMatchRule(acme) + rest
			if r.TLS != nil {
				tls := *r.TLS
				tls.Domains = tlsDomainsFor(acme)
				r.TLS = &tls
			}
			cfg.TCP.Routers[name] = r
		}
	}

	s.addCFTunnelPanelRouters(cfg)
}

// addCFTunnelPanelRouters serves the control panel on `<base>` and
// `app.<base>` for every tunnel base (the two hosts the panel is conventionally
// reached on; see docs/docs/configuration.md "Multi-domain"), plus /_oauth on
// the same hosts to authservice. ACME base domains get these routers from
// docker-compose labels instead, which can't be generated per domain.
func (s *Server) addCFTunnelPanelRouters(cfg *traefikDynamicConfig) {
	if cfg.HTTP == nil {
		cfg.HTTP = &traefikHTTP{
			Routers:  make(map[string]traefikRouter),
			Services: make(map[string]traefikService),
		}
	}
	var hosts []string
	for _, d := range s.cfTunnelDomains {
		hosts = append(hosts, d, "app."+d)
	}
	rule := hostMatchRule(hosts)
	cfg.HTTP.Routers["panel"+cfTunnelRouterSuffix] = traefikRouter{
		Rule:        rule,
		EntryPoints: []string{cfTunnelEntryPoint},
		Service:     cfTunnelPanelService,
		Middlewares: []string{"strip-internal-key@file", "block-internal-api@file"},
	}
	cfg.HTTP.Routers["panel-oauth"+cfTunnelRouterSuffix] = traefikRouter{
		Rule:        rule + " && PathPrefix(`/_oauth`)",
		EntryPoints: []string{cfTunnelEntryPoint},
		Service:     deviceFlowServiceName,
		Priority:    200,
	}
	if _, ok := cfg.HTTP.Services[deviceFlowServiceName]; !ok {
		cfg.HTTP.Services[deviceFlowServiceName] = traefikService{
			LoadBalancer: traefikLB{Servers: []traefikServer{{URL: s.authServiceURL}}},
		}
	}
}
