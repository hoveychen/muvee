package main

import (
	"testing"
)

func TestTunnelDomain_Deterministic(t *testing.T) {
	// Same inputs must always produce the same output.
	d1 := tunnelDomain("/home/user/project", 8080)
	d2 := tunnelDomain("/home/user/project", 8080)
	if d1 != d2 {
		t.Fatalf("expected deterministic result, got %q and %q", d1, d2)
	}
}

func TestTunnelDomain_DifferentPort(t *testing.T) {
	d1 := tunnelDomain("/home/user/project", 8080)
	d2 := tunnelDomain("/home/user/project", 3000)
	if d1 == d2 {
		t.Fatalf("different ports should produce different domains, both got %q", d1)
	}
}

func TestTunnelDomain_DifferentCwd(t *testing.T) {
	d1 := tunnelDomain("/home/user/project-a", 8080)
	d2 := tunnelDomain("/home/user/project-b", 8080)
	if d1 == d2 {
		t.Fatalf("different cwd should produce different domains, both got %q", d1)
	}
}

func TestTunnelDomain_Format(t *testing.T) {
	d := tunnelDomain("/tmp/test", 9999)
	if len(d) < 5 { // "t-a-b" minimum
		t.Fatalf("domain too short: %q", d)
	}
	if d[:2] != "t-" {
		t.Fatalf("domain should start with 't-', got %q", d)
	}
	// Should contain exactly 2 hyphens after "t-": t-adjective-noun
	hyphens := 0
	for _, c := range d {
		if c == '-' {
			hyphens++
		}
	}
	if hyphens != 2 {
		t.Fatalf("expected 2 hyphens in %q, got %d", d, hyphens)
	}
}

func TestTunnelDomain_DNSSafe(t *testing.T) {
	d := tunnelDomain("/some/path", 1234)
	for _, c := range d {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			t.Fatalf("domain contains non-DNS-safe character %q in %q", string(c), d)
		}
	}
}
