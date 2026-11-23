package mesh

import (
	"strings"
	"testing"
)

func TestClassifyAttachment(t *testing.T) {
	fixed := func(ip string) *struct {
		IPv4Address string `json:"IPv4Address"`
	} {
		return &struct {
			IPv4Address string `json:"IPv4Address"`
		}{ip}
	}
	gw := "10.213.0.2"
	cases := []struct {
		name string
		in   *networkAttachment
		want attachState
	}{
		{"absent", nil, attachAbsent},
		{"connected at gateway", &networkAttachment{IPAddress: gw, EndpointID: "ep", IPAMConfig: fixed(gw)}, attachOK},
		{"connected elsewhere", &networkAttachment{IPAddress: "10.213.128.5", EndpointID: "ep"}, attachWrongIP},
		// Left by a failed `connect --ip`: config only, no endpoint.
		{"stale fixed-IP stub", &networkAttachment{IPAMConfig: fixed(gw)}, attachStale},
		{"stale without ipam", &networkAttachment{}, attachStale},
	}
	for _, c := range cases {
		if got := classifyAttachment(c.in, gw); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func holderString(h *gatewayHolder) string {
	if h == nil {
		return ""
	}
	return h.String()
}

func TestParseGatewayHolder(t *testing.T) {
	self := "aaaa"
	out := `aaaa|/agent|running|10.213.0.2|10.213.0.2|me
bbbb|/web|running|10.213.128.7||
cccc|/yipu-agent|exited||10.213.0.2|yipu
dddd|/other|running|||`
	if got := holderString(parseGatewayHolder(out, self, "10.213.0.2")); got != "yipu-agent (exited, fixed IP reserved, compose project yipu)" {
		t.Errorf("stopped reservation: got %q", got)
	}
	running := "eeee|/yipu-agent|running|10.213.0.2|10.213.0.2|yipu"
	h := parseGatewayHolder(running, self, "10.213.0.2")
	if got := holderString(h); got != "yipu-agent (running, compose project yipu)" || !h.Live {
		t.Errorf("running holder: got %q", got)
	}
	// Only self (and unrelated containers) on the IP: nobody else holds it.
	if got := parseGatewayHolder("aaaa|/agent|running||10.213.0.2|me\n"+"bbbb|/web|running|10.213.128.7||", self, "10.213.0.2"); got != nil {
		t.Errorf("self must not count as holder: got %q", got)
	}
	if got := parseGatewayHolder("", self, "10.213.0.2"); got != nil {
		t.Errorf("empty: got %q", got)
	}
	// No compose project: identity falls back to the container name.
	if got := holderString(parseGatewayHolder("ffff|/squatter|running|10.213.0.2||", self, "10.213.0.2")); got != "squatter (running, container squatter)" {
		t.Errorf("no project: got %q", got)
	}
}

// M2: a running container is judged by its live IP only. Its config-only
// leftover (IPAMConfig set, no IPAddress: the insnap agent on io) does not
// hold the gateway; stopped / created containers still reserve theirs.
func TestParseGatewayHolderRunningLeftoverIsNotHolder(t *testing.T) {
	self, gw := "aaaa", "10.213.0.2"
	if got := parseGatewayHolder("bbbb|/insnap-agent|running||10.213.0.2|insnap", self, gw); got != nil {
		t.Errorf("running container with only a leftover entry: got %q, want no holder", got)
	}
	if got := parseGatewayHolder("bbbb|/insnap-agent|paused||10.213.0.2|insnap", self, gw); got != nil {
		t.Errorf("paused container with only a leftover entry: got %q, want no holder", got)
	}
	if got := parseGatewayHolder("cccc|/yipu-agent|exited||10.213.0.2|yipu", self, gw); got == nil {
		t.Error("stopped container's fixed IP must still count as reserved")
	}
	for _, st := range []string{"restarting", "dead"} {
		if got := parseGatewayHolder("cccc|/yipu-agent|"+st+"||10.213.0.2|yipu", self, gw); got == nil {
			t.Errorf("%s container's fixed IP must still count as reserved", st)
		}
	}
	got := parseGatewayHolder("dddd|/yipu-agent|created||10.213.0.2|yipu", self, gw)
	if got == nil || !strings.Contains(got.String(), "docker rm") {
		t.Errorf("created container reserves its IP and the message suggests docker rm: got %q", holderString(got))
	}
}

func TestParseMeshNetwork(t *testing.T) {
	n, err := parseMeshNetwork("muvee-mesh", "abc|1|10.213.0.0/16 ")
	if err != nil || n.ID != "abc" || n.Subnet != "10.213.0.0/16" {
		t.Fatalf("labelled: %+v %v", n, err)
	}
	if n, err := parseMeshNetwork("muvee-mesh", "abc|1|fd00::/64 10.9.0.0/24 "); err != nil || n.Subnet != "10.9.0.0/24" {
		t.Errorf("dual stack: %+v %v", n, err)
	}
	// M1: a stack's compose network (or any network without the label).
	for _, out := range []string{"abc||172.18.0.0/16 ", "abc|0|172.18.0.0/16 "} {
		if _, err := parseMeshNetwork("yipu_default", out); err == nil || !strings.Contains(err.Error(), "muvee.mesh=1") {
			t.Errorf("%q: want refusal naming the label, got %v", out, err)
		}
	}
	if _, err := parseMeshNetwork("m", "abc|1|"); err == nil {
		t.Error("no IPv4 subnet must be an error")
	}
}
