package mesh

import "testing"

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

func TestParseGatewayHolder(t *testing.T) {
	self := "aaaa"
	out := `aaaa|/agent|true|10.213.0.2|10.213.0.2
bbbb|/web|true|10.213.128.7|
cccc|/yipu-agent|false||10.213.0.2
dddd|/other|true||`
	if got := parseGatewayHolder(out, self, "10.213.0.2"); got != "yipu-agent (stopped, fixed IP reserved)" {
		t.Errorf("stopped reservation: got %q", got)
	}
	running := "eeee|/yipu-agent|true|10.213.0.2|10.213.0.2"
	if got := parseGatewayHolder(running, self, "10.213.0.2"); got != "yipu-agent (running)" {
		t.Errorf("running holder: got %q", got)
	}
	// Only self (and unrelated containers) on the IP: nobody else holds it.
	if got := parseGatewayHolder("aaaa|/agent|true||10.213.0.2\n"+"bbbb|/web|true|10.213.128.7|", self, "10.213.0.2"); got != "" {
		t.Errorf("self must not count as holder: got %q", got)
	}
	if got := parseGatewayHolder("", self, "10.213.0.2"); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
