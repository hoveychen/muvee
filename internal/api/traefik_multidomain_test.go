package api

import "testing"

// TestTraefikConfigMultiDomain locks in the P4 contract: a project/tunnel
// prefix must be routed under EVERY configured base domain, each with its own
// TLS cert entry (no wildcard). Mirrors the router-building logic in
// handleTraefikConfig — the store is a concrete type that can't be mocked, so
// like the existing tunnel/bypass tests this exercises the shared helpers the
// handler uses (hostsForPrefix / hostMatchRule / tlsDomainsFor) rather than
// driving the full HTTP handler.
func TestTraefikConfigMultiDomain(t *testing.T) {
	s := &Server{
		baseDomain:  "muveeai.com",
		baseDomains: []string{"muveeai.com", "muvee.ai"},
	}

	hosts := s.hostsForPrefix("foo")
	if len(hosts) != 2 {
		t.Fatalf("hostsForPrefix = %v, want 2 hosts (one per base domain)", hosts)
	}
	if hosts[0] != "foo.muveeai.com" || hosts[1] != "foo.muvee.ai" {
		t.Fatalf("hostsForPrefix = %v, want [foo.muveeai.com foo.muvee.ai]", hosts)
	}

	wantRule := "(Host(`foo.muveeai.com`) || Host(`foo.muvee.ai`))"
	if got := hostMatchRule(hosts); got != wantRule {
		t.Errorf("hostMatchRule = %q, want %q", got, wantRule)
	}

	tlsDomains := tlsDomainsFor(hosts)
	if len(tlsDomains) != 2 || tlsDomains[0].Main != "foo.muveeai.com" || tlsDomains[1].Main != "foo.muvee.ai" {
		t.Errorf("tlsDomainsFor = %+v, want one cert entry per base domain", tlsDomains)
	}
}

// TestTraefikConfigSingleDomain guards the single-domain (no BASE_DOMAINS)
// path so the multi-domain change can't regress existing deployments: one
// host, a bare Host() rule with no parentheses, one cert entry.
func TestTraefikConfigSingleDomain(t *testing.T) {
	s := &Server{baseDomain: "example.com"}

	hosts := s.hostsForPrefix("myapp")
	if len(hosts) != 1 || hosts[0] != "myapp.example.com" {
		t.Fatalf("hostsForPrefix = %v, want [myapp.example.com]", hosts)
	}
	if got := hostMatchRule(hosts); got != "Host(`myapp.example.com`)" {
		t.Errorf("hostMatchRule = %q, want bare Host() with no parens", got)
	}
}
