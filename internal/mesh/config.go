// Package mesh implements the deploy agent's side of muvee-mesh: a docker
// network shared by project containers on this node, with the agent container
// attached at a fixed IP acting as DNS server and HTTP gateway. A project
// container calls http://<target domain_prefix>.muvee.internal/...; the gateway
// identifies the caller by source IP, asks the control plane whether the call is
// allowed, and forwards it to the target's published port with caller headers.
package mesh

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

const (
	// NetworkName is the docker network every mesh participant joins.
	NetworkName = "muvee-mesh"
	// Zone is the DNS suffix answered by the gateway.
	Zone = "muvee.internal"

	defaultSubnet    = "10.213.0.0/16"
	defaultGatewayIP = "10.213.0.2"

	// HeaderCallerProject / HeaderCallerProjectID are injected into every
	// request forwarded by the gateway.
	HeaderCallerProject   = "X-Muvee-Caller-Project"
	HeaderCallerProjectID = "X-Muvee-Caller-Project-Id"
)

// Config is the agent-side mesh configuration. Zero values fall back to the
// defaults; see ConfigFromEnv.
type Config struct {
	// Subnet is used only when the network has to be created. An existing
	// network is reused as-is and its actual subnet wins.
	Subnet string
	// GatewayIP is the fixed IP the agent container takes on the network.
	// Empty means "10.213.0.2", or subnet base+2 when the reused network's
	// subnet does not contain the default.
	GatewayIP string
	// FallbackDNS are handed to project containers as secondary --dns entries
	// so name resolution keeps working while the gateway is down. Empty means
	// auto-detect from the agent's /etc/resolv.conf.
	FallbackDNS []string
	// SelfContainer overrides auto-detection of the agent's own container.
	SelfContainer string

	ControlPlaneURL string
	AgentSecret     string
}

// ConfigFromEnv reads MESH_SUBNET / MESH_GATEWAY_IP / MESH_FALLBACK_DNS
// (comma-separated) / MESH_SELF_CONTAINER.
func ConfigFromEnv(controlPlaneURL, agentSecret string) Config {
	cfg := Config{
		Subnet:          strings.TrimSpace(os.Getenv("MESH_SUBNET")),
		GatewayIP:       strings.TrimSpace(os.Getenv("MESH_GATEWAY_IP")),
		SelfContainer:   strings.TrimSpace(os.Getenv("MESH_SELF_CONTAINER")),
		ControlPlaneURL: controlPlaneURL,
		AgentSecret:     agentSecret,
	}
	for _, s := range strings.Split(os.Getenv("MESH_FALLBACK_DNS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			cfg.FallbackDNS = append(cfg.FallbackDNS, s)
		}
	}
	return cfg
}

// pickGatewayIP returns the gateway IP to use inside subnet. An explicit
// value must lie inside the subnet; otherwise the default is used when it
// fits, else the subnet's base address + 2 (base+1 is the bridge itself).
func pickGatewayIP(subnet, explicit string) (string, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse subnet %q: %w", subnet, err)
	}
	if explicit != "" {
		ip := net.ParseIP(explicit)
		if ip == nil || !ipnet.Contains(ip) {
			return "", fmt.Errorf("MESH_GATEWAY_IP %s is not inside mesh subnet %s", explicit, subnet)
		}
		return ip.String(), nil
	}
	if ipnet.Contains(net.ParseIP(defaultGatewayIP)) {
		return defaultGatewayIP, nil
	}
	base := ipnet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("mesh subnet %s is not IPv4", subnet)
	}
	ip := make(net.IP, 4)
	copy(ip, base)
	ip[3] += 2
	return ip.String(), nil
}

// dynamicIPRange returns the half of subnet that does not contain gatewayIP,
// passed as --ip-range on network creation so docker never hands the
// gateway's fixed IP to a project container while the agent is restarting.
func dynamicIPRange(subnet, gatewayIP string) (string, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", err
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 29 {
		return "", fmt.Errorf("mesh subnet %s too small or not IPv4", subnet)
	}
	half := net.CIDRMask(ones+1, 32)
	lower := &net.IPNet{IP: ipnet.IP.Mask(half), Mask: half}
	upperIP := make(net.IP, 4)
	copy(upperIP, ipnet.IP.To4())
	// Set the first host bit to select the upper half.
	upperIP[(ones)/8] |= 0x80 >> (ones % 8)
	upper := &net.IPNet{IP: upperIP, Mask: half}
	if lower.Contains(net.ParseIP(gatewayIP)) {
		return upper.String(), nil
	}
	return lower.String(), nil
}

// resolvConf holds the nameservers the agent itself uses and the upstream
// servers docker recorded for its embedded resolver.
type resolvConf struct {
	Nameservers []string
	// ExtServers are parsed from docker's "# ExtServers: [host(1.2.3.4) 5.6.7.8]"
	// comment, written when the container uses the embedded 127.0.0.11 DNS.
	ExtServers []string
}

func readResolvConf(path string) resolvConf {
	f, err := os.Open(path)
	if err != nil {
		return resolvConf{}
	}
	defer f.Close()
	return parseResolvConf(f)
}

func parseResolvConf(r io.Reader) resolvConf {
	var rc resolvConf
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "nameserver" {
			rc.Nameservers = append(rc.Nameservers, fields[1])
			continue
		}
		if rest, ok := strings.CutPrefix(line, "# ExtServers:"); ok {
			rest = strings.Trim(strings.TrimSpace(rest), "[]")
			for _, s := range strings.Fields(rest) {
				s = strings.TrimSuffix(strings.TrimPrefix(s, "host("), ")")
				rc.ExtServers = append(rc.ExtServers, s)
			}
		}
	}
	return rc
}

// upstreams returns where the gateway forwards non-mesh queries. The agent's
// own resolvers are used verbatim, including docker's embedded 127.0.0.11:
// the gateway runs in the agent's network namespace, where it is reachable.
func (rc resolvConf) upstreams() []string {
	out := make([]string, 0, len(rc.Nameservers))
	for _, ns := range rc.Nameservers {
		out = append(out, net.JoinHostPort(ns, "53"))
	}
	return out
}

// fallbackDNS returns resolvers usable from inside a project container:
// loopback addresses (docker's 127.0.0.11, systemd-resolved's 127.0.0.53)
// only exist in their own namespace and are dropped.
func (rc resolvConf) fallbackDNS() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range append(append([]string{}, rc.Nameservers...), rc.ExtServers...) {
		ip := net.ParseIP(s)
		if ip == nil || ip.IsLoopback() || seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		out = append(out, ip.String())
	}
	return out
}
