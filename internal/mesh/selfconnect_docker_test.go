package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests drive a real docker daemon. They are skipped unless
// TEST_DOCKER_MESH=1 (and docker is reachable), mirroring the
// TEST_DATABASE_URL gate of the *_pg_test.go files. Everything they create is
// named <prefix><random>* (prefix TEST_DOCKER_MESH_PREFIX, default "mt") and
// removed on cleanup; they need the busybox image.
//
//	TEST_DOCKER_MESH=1 go test ./internal/mesh/ -run Docker -v

const dockerTestImage = "busybox:latest"

type dockerFixture struct {
	t       *testing.T
	ctx     context.Context
	network string
	subnet  string
	gw      string
	// holders is the gateway's foreign-holder memory, persisted under a
	// per-test temp dir.
	holders  *holderMemory
	stateDir string
}

// meshNet resolves the test network the way ensureNetwork would (minus the
// label check, which the direct ensureSelfConnected tests do not need).
func (f *dockerFixture) meshNet() meshNetwork {
	f.t.Helper()
	id, err := dockerCmd(f.ctx, "network", "inspect", "-f", "{{.Id}}", f.network)
	if err != nil {
		f.t.Fatalf("inspect network: %v", err)
	}
	return meshNetwork{Name: f.network, ID: id, Subnet: f.subnet}
}

func newDockerFixture(t *testing.T) *dockerFixture {
	t.Helper()
	return newDockerFixtureNet(t)
}

// newMeshFixture creates the test network the way the agent does, with the
// muvee.mesh=1 label that marks it as a mesh network.
func newMeshFixture(t *testing.T) *dockerFixture {
	t.Helper()
	return newDockerFixtureNet(t, "--label", "muvee.mesh=1")
}

func newDockerFixtureNet(t *testing.T, netFlags ...string) *dockerFixture {
	t.Helper()
	if os.Getenv("TEST_DOCKER_MESH") != "1" {
		t.Skip("set TEST_DOCKER_MESH=1 to run docker-backed mesh tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	if _, err := dockerCmd(ctx, "info", "-f", "{{.ServerVersion}}"); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
	prefix := os.Getenv("TEST_DOCKER_MESH_PREFIX")
	if prefix == "" {
		prefix = "mt"
	}
	f := &dockerFixture{t: t, ctx: ctx, network: fmt.Sprintf("%s%d", prefix, rand.Intn(1_000_000)), stateDir: t.TempDir()}
	f.holders = newHolderMemory(f.stateDir)
	// Pick a free /24 inside 10.249.0.0/16; docker rejects overlapping pools.
	var lastErr error
	for i := 0; i < 20; i++ {
		f.subnet = fmt.Sprintf("10.249.%d.0/24", rand.Intn(250))
		if _, lastErr = dockerCmd(ctx, append(append([]string{"network", "create", "--subnet", f.subnet}, netFlags...), f.network)...); lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		t.Fatalf("create test network: %v", lastErr)
	}
	f.gw = strings.TrimSuffix(f.subnet, "0/24") + "2"
	t.Cleanup(func() {
		ids, _ := dockerCmd(context.Background(), "ps", "-aq", "--filter", "name=^"+f.network)
		if ids != "" {
			_, _ = dockerCmd(context.Background(), append([]string{"rm", "-f"}, strings.Fields(ids)...)...)
		}
		_, _ = dockerCmd(context.Background(), "network", "rm", f.network)
	})
	return f
}

// run starts a long-sleeping container; extra are docker run flags.
func (f *dockerFixture) run(suffix string, extra ...string) string {
	f.t.Helper()
	name := f.network + suffix
	args := append([]string{"run", "-d", "--name", name}, extra...)
	args = append(args, dockerTestImage, "sleep", "600")
	if _, err := dockerCmd(f.ctx, args...); err != nil {
		f.t.Fatalf("docker run %s: %v", name, err)
	}
	return name
}

// meshEntry returns the container's NetworkSettings entry for the test
// network (nil when absent).
func (f *dockerFixture) meshEntry(container string) *networkAttachment {
	f.t.Helper()
	out, err := dockerCmd(f.ctx, "inspect", "-f", "{{json .NetworkSettings.Networks}}", container)
	if err != nil {
		f.t.Fatalf("inspect %s: %v", container, err)
	}
	var nets map[string]*networkAttachment
	if err := json.Unmarshal([]byte(out), &nets); err != nil {
		f.t.Fatalf("parse networks: %v", err)
	}
	return nets[f.network]
}

// assertRestartable checks the property the fix is about: whatever mesh did,
// the container carries no fixed-IP leftover and a plain restart works.
func (f *dockerFixture) assertRestartable(container string) {
	f.t.Helper()
	if e := f.meshEntry(container); e != nil && e.IPAMConfig != nil && e.IPAMConfig.IPv4Address != "" && e.EndpointID == "" {
		f.t.Errorf("%s keeps a stale fixed-IP entry on %s: %+v (IPAMConfig %+v)", container, f.network, *e, *e.IPAMConfig)
	}
	if _, err := dockerCmd(f.ctx, "restart", "-t", "0", container); err != nil {
		f.t.Errorf("docker restart %s failed: %v", container, err)
	}
}

func TestDockerSelfConnectGatewayTakenByRunningContainer(t *testing.T) {
	f := newDockerFixture(t)
	f.run("holder", "--network", f.network, "--ip", f.gw)
	target := f.run("agent")

	err := ensureSelfConnectedForTest(f, target)
	if err == nil {
		t.Fatal("ensureSelfConnected succeeded although the gateway IP is taken")
	}
	t.Logf("error: %v", err)
	if !strings.Contains(err.Error(), f.network+"holder") || !strings.Contains(err.Error(), "MESH_NETWORK") {
		t.Errorf("error should name the holder and point at MESH_NETWORK/MESH_SUBNET: %v", err)
	}
	f.assertRestartable(target)
}

// The io incident: an older agent already left a stale fixed-IP entry behind;
// the new code must clear it even though the IP is still taken.
func TestDockerSelfConnectClearsPreexistingStaleEntry(t *testing.T) {
	f := newDockerFixture(t)
	f.run("holder", "--network", f.network, "--ip", f.gw)
	target := f.run("agent")
	if _, err := dockerCmd(f.ctx, "network", "connect", "--ip", f.gw, f.network, target); err == nil {
		t.Fatal("setup: expected connect --ip to collide")
	}
	if e := f.meshEntry(target); e == nil || e.EndpointID != "" {
		t.Fatalf("setup: expected a stale entry after the failed connect, got %+v", e)
	}

	if err := ensureSelfConnectedForTest(f, target); err == nil {
		t.Fatal("ensureSelfConnected succeeded although the gateway IP is taken")
	}
	f.assertRestartable(target)
}

// A stopped container still reserves its fixed IP: taking it would make that
// container (e.g. another stack's agent) fail its next start.
func TestDockerSelfConnectRespectsStoppedHolder(t *testing.T) {
	f := newDockerFixture(t)
	holder := f.run("holder", "--network", f.network, "--ip", f.gw)
	if _, err := dockerCmd(f.ctx, "stop", "-t", "0", holder); err != nil {
		t.Fatal(err)
	}
	target := f.run("agent")

	err := ensureSelfConnectedForTest(f, target)
	if err == nil {
		t.Fatal("ensureSelfConnected took an IP reserved by a stopped container")
	}
	t.Logf("error: %v", err)
	f.assertRestartable(target)
	if _, err := dockerCmd(f.ctx, "start", holder); err != nil {
		t.Errorf("stopped holder can no longer start: %v", err)
	}
}

func TestDockerSelfConnectHappyPathAndIdempotent(t *testing.T) {
	f := newDockerFixture(t)
	target := f.run("agent")
	for i := 0; i < 2; i++ {
		if err := ensureSelfConnectedForTest(f, target); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	if _, err := dockerCmd(f.ctx, "restart", "-t", "0", target); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if e := f.meshEntry(target); e == nil || e.IPAddress != f.gw {
		t.Fatalf("after restart want IP %s, got %+v", f.gw, e)
	}
}

// Recovery once the conflict is gone (e.g. the other stack moved to its own
// MESH_NETWORK): a stale stub from an earlier failure is replaced by a real
// attachment at the gateway IP.
func TestDockerSelfConnectRecoversAfterHolderLeaves(t *testing.T) {
	f := newDockerFixture(t)
	holder := f.run("holder", "--network", f.network, "--ip", f.gw)
	target := f.run("agent")
	if _, err := dockerCmd(f.ctx, "network", "connect", "--ip", f.gw, f.network, target); err == nil {
		t.Fatal("setup: expected connect --ip to collide")
	}
	if _, err := dockerCmd(f.ctx, "rm", "-f", holder); err != nil {
		t.Fatal(err)
	}
	if err := ensureSelfConnectedForTest(f, target); err != nil {
		t.Fatalf("ensureSelfConnected: %v", err)
	}
	if _, err := dockerCmd(f.ctx, "restart", "-t", "0", target); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if e := f.meshEntry(target); e == nil || e.IPAddress != f.gw {
		t.Fatalf("want IP %s after recovery, got %+v", f.gw, e)
	}
}
