package deployer

import (
	"fmt"
	"strings"
)

// MeshSpec attaches a project container to the node's muvee-mesh network so
// it can reach sibling projects at <prefix>.muvee.internal through the agent's
// gateway. GatewayIP is the first resolver (it answers muvee.internal and
// forwards the rest); FallbackDNS keeps external lookups working while the
// gateway is down (e.g. the agent is being recreated). nil means no mesh.
type MeshSpec struct {
	Network     string
	GatewayIP   string
	FallbackDNS []string
}

func (m *MeshSpec) dnsServers() []string {
	return append([]string{m.GatewayIP}, m.FallbackDNS...)
}

// meshRunArgs returns the docker run flags that put the container on the mesh.
func meshRunArgs(m *MeshSpec) []string {
	if m == nil {
		return nil
	}
	args := []string{"--network", m.Network}
	for _, d := range m.dnsServers() {
		args = append(args, "--dns", d)
	}
	return args
}

// meshComposeServiceYAML returns the networks/dns keys for the expose service
// (indented under it). `default` is listed explicitly because declaring any
// networks on a service otherwise drops its implicit default network, which
// the user's other services use to reach it.
func meshComposeServiceYAML(m *MeshSpec) string {
	var b strings.Builder
	b.WriteString("    networks:\n      - default\n")
	fmt.Fprintf(&b, "      - %s\n", m.Network)
	b.WriteString("    dns:\n")
	for _, d := range m.dnsServers() {
		fmt.Fprintf(&b, "      - \"%s\"\n", d)
	}
	return b.String()
}

// meshComposeNetworksYAML declares the mesh network as external at top level.
func meshComposeNetworksYAML(m *MeshSpec) string {
	return fmt.Sprintf("networks:\n  %s:\n    external: true\n", m.Network)
}
