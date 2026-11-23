package mesh

import (
	"strings"
	"testing"
)

// Round-2 regressions (BACKLOG-20261124-001 review): M1 (never disconnect the
// agent from a network that is not a mesh network / before knowing the
// gateway IP is free), M2 (a running container's config-only leftover is not
// a holder) and A′ (remember a foreign stack's holder across its Watchtower
// recreate gap). Same TEST_DOCKER_MESH gate as selfconnect_docker_test.go.

// M1: MESH_NETWORK mistakenly names a stack network the agent is already on
// (e.g. its compose network). Mesh must refuse it and leave the agent's
// existing attachment untouched.
func TestDockerMeshRefusesUnlabelledNetworkAgentIsOn(t *testing.T) {
	f := newDockerFixture(t) // plain network, no muvee.mesh=1 label
	f.run("holder", "--network", f.network, "--ip", f.gw)
	agent := f.run("agent", "--network", f.network)
	before := f.meshEntry(agent)
	if before == nil || before.EndpointID == "" {
		t.Fatalf("setup: agent not attached: %+v", before)
	}

	err := reconcileOnce(f, agent)
	if err == nil {
		t.Error("mesh accepted a network without the muvee.mesh=1 label")
	} else {
		t.Logf("error: %v", err)
	}
	after := f.meshEntry(agent)
	if after == nil || after.EndpointID != before.EndpointID || after.IPAddress != before.IPAddress {
		t.Errorf("agent's attachment to %s was changed: before %+v after %+v", f.network, before, after)
	}
}

// M1 (order): on a real mesh network, an agent attached at a wrong IP must not
// be disconnected while the gateway IP is held by someone else.
func TestDockerSelfConnectWrongIPKeptWhileGatewayHeld(t *testing.T) {
	f := newMeshFixture(t)
	f.run("holder", "--network", f.network, "--ip", f.gw)
	agent := f.run("agent", "--network", f.network)
	before := f.meshEntry(agent)

	if err := ensureSelfConnectedForTest(f, agent); err == nil {
		t.Fatal("ensureSelfConnected succeeded although the gateway IP is taken")
	}
	after := f.meshEntry(agent)
	if after == nil || after.EndpointID != before.EndpointID {
		t.Errorf("agent was disconnected before the holder check: before %+v after %+v", before, after)
	}
}

// M2: another running container that only carries a config-only leftover for
// the gateway IP (the insnap agent on io) does not hold it.
func TestDockerSelfConnectIgnoresRunningContainerWithStaleEntry(t *testing.T) {
	f := newMeshFixture(t)
	holder := f.run("holder", "--network", f.network, "--ip", f.gw)
	stale := f.run("stale")
	if _, err := dockerCmd(f.ctx, "network", "connect", "--ip", f.gw, f.network, stale); err == nil {
		t.Fatal("setup: expected connect --ip to collide")
	}
	if _, err := dockerCmd(f.ctx, "rm", "-f", holder); err != nil {
		t.Fatal(err)
	}
	if e := f.meshEntry(stale); e == nil || e.EndpointID != "" {
		t.Fatalf("setup: expected a stale entry on %s, got %+v", stale, e)
	}
	agent := f.run("agent")
	if err := ensureSelfConnectedForTest(f, agent); err != nil {
		t.Fatalf("running container with only a leftover entry blocked the gateway: %v", err)
	}
	if e := f.meshEntry(agent); e == nil || e.IPAddress != f.gw {
		t.Fatalf("want agent at %s, got %+v", f.gw, e)
	}
}

// A′: a foreign stack's agent holds the gateway IP. While Watchtower recreates
// it (old container removed, new one not yet started) the IP looks free; we
// must not grab it, or the other stack's new agent stays stuck in Created.
func TestDockerSelfConnectRemembersForeignHolder(t *testing.T) {
	f := newMeshFixture(t)
	holder := f.run("holder", "--network", f.network, "--ip", f.gw,
		"--label", "com.docker.compose.project="+f.network+"other")
	agent := f.run("agent", "--label", "com.docker.compose.project="+f.network+"self")

	err := ensureSelfConnectedForTest(f, agent)
	if err == nil {
		t.Fatal("ensureSelfConnected succeeded although the gateway IP is taken")
	}
	if !strings.Contains(err.Error(), f.network+"other") {
		t.Errorf("error should name the other stack's compose project: %v", err)
	}
	if _, err := dockerCmd(f.ctx, "rm", "-f", holder); err != nil {
		t.Fatal(err)
	}
	// Recreate gap: holder gone, its replacement not yet created.
	if err := ensureSelfConnectedForTest(f, agent); err == nil {
		t.Fatal("took the gateway IP during the foreign holder's recreate gap")
	} else {
		t.Logf("gap error: %v", err)
	}
	if e := f.meshEntry(agent); e != nil {
		t.Errorf("agent must stay off %s, got %+v", f.network, e)
	}
	// The agent process restarts (container recreated, DATA_DIR kept): it
	// still remembers.
	f.holders = newHolderMemory(f.stateDir)
	if err := ensureSelfConnectedForTest(f, agent); err == nil {
		t.Fatal("forgot the foreign holder across an agent restart")
	}
	// The other stack's recreated agent can still claim its IP.
	if _, err := dockerCmd(f.ctx, "run", "-d", "--name", f.network+"holder2", "--network", f.network, "--ip", f.gw,
		dockerTestImage, "sleep", "600"); err != nil {
		t.Errorf("foreign stack's recreated agent cannot take its IP back: %v", err)
	}
}

// A′: the memory is tied to the network ID. Once the network is recreated
// (e.g. the other stack moved to its own MESH_NETWORK and this one was
// rebuilt), the agent claims the gateway again.
func TestDockerSelfConnectForeignHolderForgottenWhenNetworkRecreated(t *testing.T) {
	f := newMeshFixture(t)
	holder := f.run("holder", "--network", f.network, "--ip", f.gw,
		"--label", "com.docker.compose.project="+f.network+"other")
	agent := f.run("agent", "--label", "com.docker.compose.project="+f.network+"self")
	if err := ensureSelfConnectedForTest(f, agent); err == nil {
		t.Fatal("setup: expected the gateway to be taken")
	}
	if _, err := dockerCmd(f.ctx, "rm", "-f", holder); err != nil {
		t.Fatal(err)
	}
	if _, err := dockerCmd(f.ctx, "network", "rm", f.network); err != nil {
		t.Fatal(err)
	}
	if _, err := dockerCmd(f.ctx, "network", "create", "--subnet", f.subnet, "--label", "muvee.mesh=1", f.network); err != nil {
		t.Fatal(err)
	}
	if err := reconcileOnce(f, agent); err != nil {
		t.Fatalf("recreated network: %v", err)
	}
	if e := f.meshEntry(agent); e == nil || e.IPAddress != f.gw {
		t.Fatalf("want agent at %s, got %+v", f.gw, e)
	}
}

// A same-stack holder (e.g. a stopped leftover of this stack's own agent) is
// not remembered: once it is gone the agent takes the gateway.
func TestDockerSelfConnectSameStackHolderNotRemembered(t *testing.T) {
	f := newMeshFixture(t)
	project := "com.docker.compose.project=" + f.network + "self"
	holder := f.run("holder", "--network", f.network, "--ip", f.gw, "--label", project)
	agent := f.run("agent", "--label", project)
	if err := ensureSelfConnectedForTest(f, agent); err == nil {
		t.Fatal("setup: expected the gateway to be taken")
	}
	if _, err := dockerCmd(f.ctx, "rm", "-f", holder); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfConnectedForTest(f, agent); err != nil {
		t.Fatalf("same-stack holder must not be remembered: %v", err)
	}
}

// M1 happy path through ensureNetwork: a labelled network is reused, and a
// missing one is created with the label.
func TestDockerEnsureNetworkCreatesLabelled(t *testing.T) {
	f := newMeshFixture(t)
	if _, err := dockerCmd(f.ctx, "network", "rm", f.network); err != nil {
		t.Fatal(err)
	}
	mn, err := ensureNetwork(f.ctx, f.network, f.subnet, f.gw)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if mn.Subnet != f.subnet || mn.ID == "" {
		t.Errorf("created network: %+v", mn)
	}
	again, err := ensureNetwork(f.ctx, f.network, "10.250.0.0/24", f.gw)
	if err != nil || again.ID != mn.ID || again.Subnet != f.subnet {
		t.Errorf("reuse: %+v %v (want ID %s subnet %s)", again, err, mn.ID, f.subnet)
	}
	if lbl, _ := dockerCmd(f.ctx, "network", "inspect", "-f", `{{index .Labels "muvee.mesh"}}`, f.network); lbl != "1" {
		t.Errorf("label muvee.mesh = %q, want 1", lbl)
	}
}

// A′ identity fallback: neither container comes from compose (docker run), so
// stacks are told apart by container name. A container without any label
// must not read as an empty/"<no value>" project shared with the agent.
func TestDockerSelfConnectRemembersForeignHolderWithoutComposeLabels(t *testing.T) {
	f := newMeshFixture(t)
	holder := f.run("holder", "--network", f.network, "--ip", f.gw)
	agent := f.run("agent")
	err := ensureSelfConnectedForTest(f, agent)
	if err == nil {
		t.Fatal("setup: expected the gateway to be taken")
	}
	if strings.Contains(err.Error(), "no value") {
		t.Errorf("label lookup leaked a template placeholder: %v", err)
	}
	if _, err := dockerCmd(f.ctx, "rm", "-f", holder); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfConnectedForTest(f, agent); err == nil {
		t.Fatal("took the gateway IP of a differently named non-compose container after it left")
	}
}
