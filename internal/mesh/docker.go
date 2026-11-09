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
func ensureNetwork(ctx context.Context, subnet, gatewayIP string) (string, error) {
	if actual, err := networkSubnet(ctx); err == nil {
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
	args = append(args, NetworkName)
	if _, err := dockerCmd(ctx, args...); err != nil {
		// Lost a race with another creator: reuse whatever won.
		if actual, err2 := networkSubnet(ctx); err2 == nil {
			return actual, nil
		}
		return "", err
	}
	return subnet, nil
}

func networkSubnet(ctx context.Context) (string, error) {
	out, err := dockerCmd(ctx, "network", "inspect", "-f", "{{range .IPAM.Config}}{{.Subnet}} {{end}}", NetworkName)
	if err != nil {
		return "", err
	}
	for _, s := range strings.Fields(out) {
		if !strings.Contains(s, ":") { // first IPv4 subnet
			return s, nil
		}
	}
	return "", fmt.Errorf("network %s has no user-configured IPv4 subnet (fixed gateway IP needs one; recreate it with --subnet)", NetworkName)
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

// ensureSelfConnected attaches container to the mesh network at gatewayIP.
// Idempotent: a connection with the right IP is left alone, a wrong one is
// replaced.
func ensureSelfConnected(ctx context.Context, container, gatewayIP string) error {
	out, err := dockerCmd(ctx, "inspect", "-f", "{{json .NetworkSettings.Networks}}", container)
	if err != nil {
		return err
	}
	var nets map[string]struct {
		IPAddress  string `json:"IPAddress"`
		IPAMConfig *struct {
			IPv4Address string `json:"IPv4Address"`
		} `json:"IPAMConfig"`
	}
	if err := json.Unmarshal([]byte(out), &nets); err != nil {
		return fmt.Errorf("parse networks of %s: %w", container, err)
	}
	if n, ok := nets[NetworkName]; ok {
		if n.IPAddress == gatewayIP {
			return nil
		}
		if _, err := dockerCmd(ctx, "network", "disconnect", "-f", NetworkName, container); err != nil {
			return err
		}
	}
	_, err = dockerCmd(ctx, "network", "connect", "--ip", gatewayIP, NetworkName, container)
	return err
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
