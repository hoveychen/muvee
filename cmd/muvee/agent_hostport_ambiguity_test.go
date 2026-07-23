package main

import "testing"

// suppressAmbiguousHostPorts guards the routing-critical host_port heartbeat:
// when a stale orphan container shares a domain_prefix with the live one, the
// server's domain_prefix-keyed refresh would let the orphan's port overwrite
// the live deployment and mis-route Traefik. The agent must not report a
// host_port it cannot attribute to a single container.

func TestSuppressAmbiguousHostPorts_ZeroesDuplicatedPrefix(t *testing.T) {
	in := []containerStatus{
		{DomainPrefix: "fleet-relay", HostPort: 48749, RestartCount: 2, OOMKilled: true}, // orphan
		{DomainPrefix: "fleet-relay", HostPort: 48764},                                   // current
		{DomainPrefix: "pixel", HostPort: 48765},                                         // unambiguous
	}
	out := suppressAmbiguousHostPorts(in)

	for _, s := range out {
		if s.DomainPrefix == "fleet-relay" && s.HostPort != 0 {
			t.Errorf("ambiguous prefix fleet-relay should report HostPort=0, got %d", s.HostPort)
		}
		if s.DomainPrefix == "pixel" && s.HostPort != 48765 {
			t.Errorf("unambiguous prefix pixel should keep HostPort=48765, got %d", s.HostPort)
		}
	}

	// restart_count / oom_killed must survive — only host_port is suppressed.
	var sawOrphanMeta bool
	for _, s := range out {
		if s.DomainPrefix == "fleet-relay" && s.RestartCount == 2 && s.OOMKilled {
			sawOrphanMeta = true
		}
	}
	if !sawOrphanMeta {
		t.Error("restart_count / oom_killed should be preserved for ambiguous prefixes")
	}
}

func TestSuppressAmbiguousHostPorts_LeavesUniquePrefixesAlone(t *testing.T) {
	in := []containerStatus{
		{DomainPrefix: "a", HostPort: 100},
		{DomainPrefix: "b", HostPort: 200},
	}
	out := suppressAmbiguousHostPorts(in)
	if out[0].HostPort != 100 || out[1].HostPort != 200 {
		t.Errorf("unique prefixes must be untouched, got %d,%d", out[0].HostPort, out[1].HostPort)
	}
}
