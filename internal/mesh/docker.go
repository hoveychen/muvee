package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// dockerCmd runs a docker CLI command and returns trimmed stdout; stderr is
// folded into the error.
func dockerCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// ensureNetwork makes sure the mesh network exists and returns its subnet.
// A network that already exists is reused and its actual subnet is returned.
func ensureNetwork(ctx context.Context, network, subnet, gatewayIP string) (string, error) {
	if actual, err := networkSubnet(ctx, network); err == nil {
		return actual, nil
	}
	args := []string{"network", "create", "--driver", "bridge", "--subnet", subnet,
		"--label", "muvee.mesh=1"}
	if r, err := dynamicIPRange(subnet, gatewayIP); err == nil {
		args = append(args, "--ip-range", r)
		// With --ip-range docker would otherwise put the bridge on the first
		// address of that range (e.g. 10.213.128.0); pin it to base+1.
		if bridge := bridgeIP(subnet); bridge != "" && bridge != gatewayIP {
			args = append(args, "--gateway", bridge)
		}
	}
	args = append(args, network)
	if _, err := dockerCmd(ctx, args...); err != nil {
		// Lost a race with another creator: reuse whatever won.
		if actual, err2 := networkSubnet(ctx, network); err2 == nil {
			return actual, nil
		}
		return "", err
	}
	return subnet, nil
}

func networkSubnet(ctx context.Context, network string) (string, error) {
	out, err := dockerCmd(ctx, "network", "inspect", "-f", "{{range .IPAM.Config}}{{.Subnet}} {{end}}", network)
	if err != nil {
		return "", err
	}
	for _, s := range strings.Fields(out) {
		if !strings.Contains(s, ":") { // first IPv4 subnet
			return s, nil
		}
	}
	return "", fmt.Errorf("network %s has no user-configured IPv4 subnet (fixed gateway IP needs one; recreate it with --subnet)", network)
}

var containerIDInMountinfo = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// detectSelfContainer returns the agent's own container ID, or "" when the
// agent is not running inside a docker container (e.g. local development).
// docker bind-mounts /etc/hostname, /etc/hosts and /etc/resolv.conf from
// /var/lib/docker/containers/<id>/, which shows up in mountinfo under both
// cgroup v1 and v2.
func detectSelfContainer(ctx context.Context) string {
	if f, err := os.Open("/proc/self/mountinfo"); err == nil {
		defer f.Close()
		if id := containerIDFromMountinfo(bufio.NewScanner(f)); id != "" {
			return id
		}
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return ""
	}
	// Inside docker but mountinfo gave nothing: the hostname is the short
	// container ID unless the compose file pinned one.
	if h, err := os.Hostname(); err == nil && h != "" {
		if id, err := dockerCmd(ctx, "inspect", "-f", "{{.Id}}", h); err == nil {
			return id
		}
	}
	return ""
}

func containerIDFromMountinfo(sc *bufio.Scanner) string {
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "/etc/hostname") && !strings.Contains(line, "/etc/resolv.conf") {
			continue
		}
		if m := containerIDInMountinfo.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// networkAttachment is one entry of a container's NetworkSettings.Networks.
type networkAttachment struct {
	IPAddress  string `json:"IPAddress"`
	EndpointID string `json:"EndpointID"`
	IPAMConfig *struct {
		IPv4Address string `json:"IPv4Address"`
	} `json:"IPAMConfig"`
}

type attachState int

const (
	attachAbsent  attachState = iota // not on the network
	attachOK                         // live endpoint at the gateway IP
	attachWrongIP                    // live endpoint at another IP
	// attachStale is a config-only entry without an endpoint. A failed
	// `docker network connect --ip` leaves one behind (docker 29), and while it
	// pins a fixed IP that someone else holds, every restart of the container
	// fails with "Address already in use".
	attachStale
)

func (s attachState) String() string {
	return [...]string{"absent", "ok", "wrong-ip", "stale"}[s]
}

// classifyAttachment judges a running container's entry for the mesh network.
func classifyAttachment(n *networkAttachment, gatewayIP string) attachState {
	switch {
	case n == nil:
		return attachAbsent
	case n.EndpointID == "" || n.IPAddress == "":
		return attachStale
	case n.IPAddress == gatewayIP:
		return attachOK
	default:
		return attachWrongIP
	}
}

// selfAttachment returns the container's full ID and its entry for network.
func selfAttachment(ctx context.Context, network, container string) (string, *networkAttachment, error) {
	out, err := dockerCmd(ctx, "inspect", "-f", "{{.Id}} {{json .NetworkSettings.Networks}}", container)
	if err != nil {
		return "", nil, err
	}
	id, raw, _ := strings.Cut(out, " ")
	var nets map[string]*networkAttachment
	if err := json.Unmarshal([]byte(raw), &nets); err != nil {
		return "", nil, fmt.Errorf("parse networks of %s: %w", container, err)
	}
	return id, nets[network], nil
}

// clearStaleAttachment removes a config-only entry. `docker network
// disconnect [-f]` refuses it ("not connected"); connecting without --ip
// (dynamic address) replaces the stub with a real endpoint, which a plain
// disconnect then removes together with the pinned IP.
func clearStaleAttachment(ctx context.Context, network, container string) error {
	if _, err := dockerCmd(ctx, "network", "connect", network, container); err != nil {
		return fmt.Errorf("clear stale %s entry on %s: %w", network, container, err)
	}
	if _, err := dockerCmd(ctx, "network", "disconnect", network, container); err != nil {
		return fmt.Errorf("clear stale %s entry on %s: %w", network, container, err)
	}
	return nil
}

// gatewayHolder returns a description of another container that holds or
// reserves ip on network ("" when none). Stopped containers count: docker
// keeps their fixed IP in their config without showing them in `network
// inspect`, and if we took the IP their next start would fail.
func gatewayHolder(ctx context.Context, network, selfID, ip string) (string, error) {
	ids, err := dockerCmd(ctx, "ps", "-aq", "--no-trunc", "--filter", "network="+network)
	if err != nil || ids == "" {
		return "", err
	}
	args := append([]string{"inspect", "-f",
		`{{.Id}}|{{.Name}}|{{.State.Running}}|{{with index .NetworkSettings.Networks "` + network +
			`"}}{{.IPAddress}}|{{with .IPAMConfig}}{{.IPv4Address}}{{end}}{{end}}`},
		strings.Fields(ids)...)
	out, err := dockerCmd(ctx, args...)
	if err != nil {
		return "", err
	}
	return parseGatewayHolder(out, selfID, ip), nil
}

// parseGatewayHolder scans "id|/name|running|ip|fixed-ip" lines.
func parseGatewayHolder(out, selfID, ip string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "|")
		if len(f) != 5 || f[0] == selfID {
			continue
		}
		name, running := strings.TrimPrefix(f[1], "/"), f[2] == "true"
		switch {
		case running && f[3] == ip:
			return name + " (running)"
		case f[4] == ip && running:
			return name + " (running, fixed IP reserved)"
		case f[4] == ip:
			return name + " (stopped, fixed IP reserved)"
		}
	}
	return ""
}

// ensureSelfConnected attaches container to network at gatewayIP.
// Idempotent: a connection with the right IP is left alone, a wrong one is
// replaced. Whatever fails, it never leaves a fixed-IP stub on container: a
// mesh problem must only keep the mesh down, never stop the agent's own
// container from restarting.
func ensureSelfConnected(ctx context.Context, network, container, gatewayIP string) error {
	selfID, n, err := selfAttachment(ctx, network, container)
	if err != nil {
		return err
	}
	switch classifyAttachment(n, gatewayIP) {
	case attachOK:
		return nil
	case attachStale:
		if err := clearStaleAttachment(ctx, network, container); err != nil {
			return err
		}
	case attachWrongIP:
		if _, err := dockerCmd(ctx, "network", "disconnect", "-f", network, container); err != nil {
			return err
		}
	}
	holder, err := gatewayHolder(ctx, network, selfID, gatewayIP)
	if err != nil {
		return err
	}
	if holder != "" {
		return fmt.Errorf("gateway IP %s on docker network %s is already held by container %s; "+
			"if another muvee stack runs on this docker host, give each stack its own MESH_NETWORK "+
			"and a non-overlapping MESH_SUBNET (mesh stays disabled until then)", gatewayIP, network, holder)
	}
	if _, err := dockerCmd(ctx, "network", "connect", "--ip", gatewayIP, network, container); err != nil {
		// Lost a race for the IP: docker may still have recorded the stub.
		if _, n2, ierr := selfAttachment(ctx, network, container); ierr == nil && classifyAttachment(n2, gatewayIP) == attachStale {
			if cerr := clearStaleAttachment(ctx, network, container); cerr != nil {
				return fmt.Errorf("%w (and %v)", err, cerr)
			}
		}
		return err
	}
	return nil
}

// containerImage returns the image a container runs; used to run a probe
// container without pulling anything.
func containerImage(ctx context.Context, container string) string {
	img, _ := dockerCmd(ctx, "inspect", "-f", "{{.Config.Image}}", container)
	return img
}

// probeDefaultBridgeDNS asks docker which upstream resolvers it gives a
// container on the default bridge. That is docker's own notion of "the host's
// upstream DNS" (it already resolves systemd-resolved's 127.0.0.53 to the
// real servers), so it is the best fallback for project containers.
func probeDefaultBridgeDNS(ctx context.Context, image string) []string {
	if image == "" {
		return nil
	}
	out, err := dockerCmd(ctx, "run", "--rm", "--network", "bridge", "--entrypoint", "cat", image, "/etc/resolv.conf")
	if err != nil {
		return nil
	}
	return parseResolvConf(strings.NewReader(out)).fallbackDNS()
}
