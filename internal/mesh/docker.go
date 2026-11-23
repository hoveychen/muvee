package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
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

// meshLabel marks a docker network as a muvee mesh network. The agent sets it
// on networks it creates and refuses to touch any network without it.
const meshLabel = "muvee.mesh"

// composeProjectLabel identifies which compose project (i.e. which muvee
// stack) a container belongs to.
const composeProjectLabel = "com.docker.compose.project"

// meshNetwork is a resolved mesh network.
type meshNetwork struct {
	Name   string
	ID     string
	Subnet string
}

// ensureNetwork makes sure the mesh network exists and returns it with its
// actual subnet. An existing network is reused only when it carries the
// muvee.mesh=1 label: a mistaken MESH_NETWORK pointing at, say, the stack's
// own compose network must keep the mesh down, never make the agent rewire
// (and possibly disconnect) its control-plane attachment.
func ensureNetwork(ctx context.Context, network, subnet, gatewayIP string) (meshNetwork, error) {
	if n, err := inspectMeshNetwork(ctx, network); err == nil || !errors.Is(err, errNoSuchNetwork) {
		return n, err
	}
	args := []string{"network", "create", "--driver", "bridge", "--subnet", subnet,
		"--label", meshLabel + "=1"}
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
		// Lost a race with another creator: reuse whatever won (if it is a
		// mesh network).
		if n, err2 := inspectMeshNetwork(ctx, network); err2 == nil || !errors.Is(err2, errNoSuchNetwork) {
			return n, err2
		}
		return meshNetwork{}, err
	}
	return inspectMeshNetwork(ctx, network)
}

var errNoSuchNetwork = errors.New("no such network")

// inspectMeshNetwork returns the network if it exists and is a mesh network;
// errNoSuchNetwork when it does not exist.
func inspectMeshNetwork(ctx context.Context, network string) (meshNetwork, error) {
	out, err := dockerCmd(ctx, "network", "inspect", "-f",
		`{{.Id}}|{{with .Labels}}{{index . "`+meshLabel+`"}}{{end}}|{{range .IPAM.Config}}{{.Subnet}} {{end}}`, network)
	if err != nil {
		// Tell "absent" (create it) from any other failure (do nothing).
		if ids, lerr := dockerCmd(ctx, "network", "ls", "-q", "--filter", "name=^"+regexp.QuoteMeta(network)+"$"); lerr == nil && ids == "" {
			return meshNetwork{}, fmt.Errorf("%w: %s", errNoSuchNetwork, network)
		}
		return meshNetwork{}, err
	}
	return parseMeshNetwork(network, out)
}

// parseMeshNetwork parses "id|mesh-label|subnet subnet ..." from inspect.
func parseMeshNetwork(network, out string) (meshNetwork, error) {
	f := strings.SplitN(out, "|", 3)
	if len(f) != 3 {
		return meshNetwork{}, fmt.Errorf("unexpected docker network inspect output for %s: %q", network, out)
	}
	if f[1] != "1" {
		return meshNetwork{}, fmt.Errorf("docker network %s exists but is not a muvee mesh network (no label %s=1); "+
			"the agent leaves it alone. MESH_NETWORK must name a network of its own, which the agent "+
			"creates (e.g. muvee-mesh-<stack>), never a compose or other shared network (mesh stays disabled until then)",
			network, meshLabel)
	}
	n := meshNetwork{Name: network, ID: f[0]}
	for _, s := range strings.Fields(f[2]) {
		if !strings.Contains(s, ":") { // first IPv4 subnet
			n.Subnet = s
			return n, nil
		}
	}
	return meshNetwork{}, fmt.Errorf("network %s has no user-configured IPv4 subnet (fixed gateway IP needs one; recreate it with --subnet)", network)
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

// Templates read labels as {{with .Labels}}{{index . "k"}}{{end}}: on a
// container or network without any label, plain index prints "<no value>".

// selfInfo is what ensureSelfConnected needs to know about the agent's own
// container.
type selfInfo struct {
	ID    string
	Stack string // stackIdentity of the agent's container
	Entry *networkAttachment
}

// selfAttachment returns the container's full ID, stack identity and its
// entry for network.
func selfAttachment(ctx context.Context, network, container string) (selfInfo, error) {
	out, err := dockerCmd(ctx, "inspect", "-f",
		`{{.Id}}|{{.Name}}|{{with .Config.Labels}}{{index . "`+composeProjectLabel+`"}}{{end}}|{{json .NetworkSettings.Networks}}`, container)
	if err != nil {
		return selfInfo{}, err
	}
	f := strings.SplitN(out, "|", 4)
	if len(f) != 4 {
		return selfInfo{}, fmt.Errorf("unexpected docker inspect output for %s: %q", container, out)
	}
	var nets map[string]*networkAttachment
	if err := json.Unmarshal([]byte(f[3]), &nets); err != nil {
		return selfInfo{}, fmt.Errorf("parse networks of %s: %w", container, err)
	}
	return selfInfo{ID: f[0], Stack: stackIdentity(f[2], strings.TrimPrefix(f[1], "/")), Entry: nets[network]}, nil
}

// stackIdentity names the muvee stack a container belongs to: its compose
// project, or — for a container not started by compose — its own name (a
// `docker run` agent keeps its name across Watchtower recreation, and any
// other container is simply not this stack).
func stackIdentity(composeProject, name string) string {
	if composeProject != "" {
		return "compose project " + composeProject
	}
	return "container " + name
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

// gatewayHolder is another container that holds or reserves the gateway IP.
type gatewayHolder struct {
	Name   string
	Status string // docker State.Status
	Stack  string // stackIdentity
	Live   bool   // has a live endpoint at the IP (vs. a fixed-IP reservation)
}

func (h *gatewayHolder) String() string {
	switch {
	case h.Live:
		return fmt.Sprintf("%s (%s, %s)", h.Name, h.Status, h.Stack)
	case h.Status == "created":
		return fmt.Sprintf("%s (created, fixed IP reserved, %s; if it is a leftover that will never start, `docker rm` it)", h.Name, h.Stack)
	default:
		return fmt.Sprintf("%s (%s, fixed IP reserved, %s)", h.Name, h.Status, h.Stack)
	}
}

// findGatewayHolder returns another container that holds or reserves ip on
// network (nil when none).
func findGatewayHolder(ctx context.Context, network, selfID, ip string) (*gatewayHolder, error) {
	ids, err := dockerCmd(ctx, "ps", "-aq", "--no-trunc", "--filter", "network="+network)
	if err != nil || ids == "" {
		return nil, err
	}
	args := append([]string{"inspect", "-f",
		`{{.Id}}|{{.Name}}|{{.State.Status}}|{{with index .NetworkSettings.Networks "` + network +
			`"}}{{.IPAddress}}|{{with .IPAMConfig}}{{.IPv4Address}}{{end}}{{else}}|{{end}}|{{with .Config.Labels}}{{index . "` +
			composeProjectLabel + `"}}{{end}}`},
		strings.Fields(ids)...)
	out, err := dockerCmd(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseGatewayHolder(out, selfID, ip), nil
}

// parseGatewayHolder scans "id|/name|status|ip|fixed-ip|compose-project"
// lines. A running (or paused) container is judged by its live IP only: a
// config-only leftover from a failed `connect --ip` on a running container
// does not hold the address (that container is typically another agent that
// clears its own leftover). Any other state — exited, created, restarting —
// still reserves its fixed IP: docker re-applies it on the next start, so
// taking it would make that start fail.
func parseGatewayHolder(out, selfID, ip string) *gatewayHolder {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "|")
		if len(f) != 6 || f[0] == selfID {
			continue
		}
		h := &gatewayHolder{Name: strings.TrimPrefix(f[1], "/"), Status: f[2],
			Stack: stackIdentity(f[5], strings.TrimPrefix(f[1], "/"))}
		switch {
		case f[3] == ip:
			h.Live = true
			return h
		case f[2] == "running" || f[2] == "paused":
			continue
		case f[4] == ip:
			return h
		}
	}
	return nil
}

// ensureSelfConnected attaches container to network at gatewayIP.
// Idempotent: a connection with the right IP is left alone, a wrong one is
// replaced. Whatever fails, it never leaves a fixed-IP stub on container: a
// mesh problem must only keep the mesh down, never stop the agent's own
// container from restarting. The existing attachment is only dropped once the
// gateway IP is known to be free.
//
// When another stack is seen holding the IP, mem remembers it for this
// network ID and later calls refuse even if the IP looks free (see
// holderMemory).
func ensureSelfConnected(ctx context.Context, net meshNetwork, container, gatewayIP string, mem *holderMemory) error {
	network := net.Name
	self, err := selfAttachment(ctx, network, container)
	if err != nil {
		return err
	}
	state := classifyAttachment(self.Entry, gatewayIP)
	switch state {
	case attachOK:
		return nil
	case attachStale:
		if err := clearStaleAttachment(ctx, network, container); err != nil {
			return err
		}
	}
	if r, ok := mem.lookup(network, net.ID); ok {
		return fmt.Errorf("gateway IP %s on docker network %s was held by container %s of another muvee stack (%s) at %s; "+
			"not taking it even though it may look free now (that stack's agent may just be being recreated, e.g. by Watchtower). "+
			"Give each stack its own MESH_NETWORK and a non-overlapping MESH_SUBNET; to retry on this network, %s (mesh stays disabled until then)",
			gatewayIP, network, r.Holder, r.Stack, r.SeenAt.Format(time.RFC3339), mem.resetHint())
	}
	holder, err := findGatewayHolder(ctx, network, self.ID, gatewayIP)
	if err != nil {
		return err
	}
	if holder != nil {
		msg := fmt.Sprintf("gateway IP %s on docker network %s is already held by container %s; "+
			"if another muvee stack runs on this docker host, give each stack its own MESH_NETWORK "+
			"and a non-overlapping MESH_SUBNET (mesh stays disabled until then)", gatewayIP, network, holder)
		if holder.Stack != self.Stack {
			rec := foreignHolder{Network: network, Holder: holder.Name, Stack: holder.Stack, SeenAt: time.Now().UTC()}
			log.Printf("mesh: WARNING: gateway IP %s on docker network %s is held by %s of another muvee stack (%s; this agent is %s). "+
				"This agent will not take that IP on this network again, even while the other stack's agent is being recreated; "+
				"to undo, %s", gatewayIP, network, holder.Name, holder.Stack, self.Stack, mem.resetHint())
			if err := mem.remember(net.ID, rec); err != nil {
				log.Printf("mesh: WARNING: %v", err)
			}
		}
		return errors.New(msg)
	}
	if state == attachWrongIP {
		if _, err := dockerCmd(ctx, "network", "disconnect", "-f", network, container); err != nil {
			return err
		}
	}
	if _, err := dockerCmd(ctx, "network", "connect", "--ip", gatewayIP, network, container); err != nil {
		// Lost a race for the IP: docker may still have recorded the stub.
		if s2, ierr := selfAttachment(ctx, network, container); ierr == nil && classifyAttachment(s2.Entry, gatewayIP) == attachStale {
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
