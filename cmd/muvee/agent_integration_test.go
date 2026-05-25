//go:build integration

package main

// Integration tests for the compose-project describe/env bug fix.
//
// Run with:
//
//	go test -v -tags=integration -count=1 ./cmd/muvee/ -run TestBugRepro
//	go test -v -tags=integration -count=1 ./cmd/muvee/ -run TestResolve
//	go test -v -tags=integration -count=1 ./cmd/muvee/ -run TestRunDescribe
//	go test -v -tags=integration -count=1 ./cmd/muvee/ -run TestRunEnvInspect
//
// Requires Docker daemon accessible to the current user.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

// randSuffix returns a unique 8-char hex string for naming test Docker objects.
func randSuffix() string {
	return fmt.Sprintf("%08x", rand.Uint32())
}

// dockerRunDetached starts a container in detached mode and registers t.Cleanup
// to force-remove it after the test. Any additional args are appended after
// "--name <name>" (e.g. --label, -e, the image, the command).
func dockerRunDetached(t *testing.T, name string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"run", "-d", "--name", name}, args...)
	out, err := exec.Command("docker", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run --name %s: %v\n%s", name, err, out)
	}
	t.Cleanup(func() {
		exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck
	})
}

// ─── Bug Reproduction ────────────────────────────────────────────────────────

// TestBugRepro_ComposeDescribeContainerName is the primary regression test.
//
// Original bug: after deploying a compose project, the describe / env tasks
// constructed the container name as "muvee-<domain_prefix>". Docker Compose
// names containers as "<project>-<service>-N"; no container with the bare
// project name exists, so docker inspect always returned exit status 1.
// The server-side completeTask handler then marked the deployment as failed
// even though all containers were healthy.
//
// This test:
//  1. Creates a container that mimics a compose expose-service
//     (different name from "muvee-<prefix>", but carries the label).
//  2. Confirms docker inspect with the old hardcoded name fails (bug).
//  3. Confirms resolveContainerName returns the correct container (fix).
func TestBugRepro_ComposeDescribeContainerName(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-compose-" + sfx

	// Compose-style container name: "<project>-<service>-<N>"
	// This is what docker compose actually creates.
	composeContainerName := "muveeproj-" + sfx + "-server-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"alpine", "sleep", "300",
	)

	// ── Part 1: confirm the bug ──────────────────────────────────────────────
	// The old code used "muvee-" + domainPrefix directly.
	// That container does not exist for compose projects.
	oldStyleName := "muvee-" + domainPrefix
	err := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.Name}}", oldStyleName).Run()
	if err == nil {
		t.Fatalf("BUG TEST SETUP ERROR: docker inspect %q succeeded — "+
			"a container with that name must not exist for the bug to be reproduced", oldStyleName)
	}
	t.Logf("BUG CONFIRMED: docker inspect %q → error (no such container): %v", oldStyleName, err)

	// ── Part 2: verify the fix ───────────────────────────────────────────────
	got := resolveContainerName(ctx, domainPrefix)
	if got != composeContainerName {
		t.Errorf("resolveContainerName(%q) = %q, want %q", domainPrefix, got, composeContainerName)
	}
	t.Logf("FIX VERIFIED: resolveContainerName(%q) = %q", domainPrefix, got)

	// Confirm the resolved name is actually inspectable.
	if out, err := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.Name}}", got).Output(); err != nil {
		t.Errorf("docker inspect %q (fixed name) still fails: %v", got, err)
	} else {
		t.Logf("docker inspect %q → %s", got, strings.TrimSpace(string(out)))
	}
}

// ─── resolveContainerName ────────────────────────────────────────────────────

// TestResolveContainerName_RegularProject verifies that resolveContainerName
// is transparent for regular projects: the container is named "muvee-<prefix>"
// and carries the label, so the lookup returns the same name as before.
func TestResolveContainerName_RegularProject(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-regular-" + randSuffix()
	containerName := "muvee-" + domainPrefix

	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"alpine", "sleep", "300",
	)

	got := resolveContainerName(ctx, domainPrefix)
	if got != containerName {
		t.Errorf("resolveContainerName(%q) = %q, want %q", domainPrefix, got, containerName)
	}
}

// TestResolveContainerName_ComposeProject verifies that resolveContainerName
// finds the compose expose-service container via the label, even though its
// name differs from "muvee-<prefix>".
func TestResolveContainerName_ComposeProject(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-cmp-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-api-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=9090",
		"alpine", "sleep", "300",
	)

	got := resolveContainerName(ctx, domainPrefix)
	if got != composeContainerName {
		t.Errorf("resolveContainerName(%q) = %q, want %q", domainPrefix, got, composeContainerName)
	}
}

// TestResolveContainerName_StoppedContainer verifies that resolveContainerName
// finds containers even when they are stopped (docker ps -a, not just docker ps).
// This matters for runDescribe, which is valid on stopped containers.
func TestResolveContainerName_StoppedContainer(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-stopped-" + sfx
	containerName := "muveeproj-" + sfx + "-worker-1"

	// "echo done" exits immediately → container is stopped after creation.
	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "echo", "done",
	)
	// Wait for it to reach exited state.
	exec.Command("docker", "wait", containerName).Run() //nolint:errcheck

	got := resolveContainerName(ctx, domainPrefix)
	if got != containerName {
		t.Errorf("resolveContainerName(%q) = %q, want %q (stopped container should be found)",
			domainPrefix, got, containerName)
	}
}

// TestResolveContainerName_NoContainer verifies the fallback: when no container
// carries the label, resolveContainerName returns "muvee-<prefix>" so that the
// subsequent docker inspect produces a "No such container" error rather than
// silently succeeding with the wrong container.
func TestResolveContainerName_NoContainer(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-absent-" + randSuffix()

	got := resolveContainerName(ctx, domainPrefix)
	want := "muvee-" + domainPrefix
	if got != want {
		t.Errorf("resolveContainerName(%q) = %q, want fallback %q", domainPrefix, got, want)
	}
}

// ─── runDescribe ─────────────────────────────────────────────────────────────

// TestRunDescribe_RegularProject verifies end-to-end that runDescribe returns
// parseable JSON with the correct container_name for a regular project.
func TestRunDescribe_RegularProject(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-desc-reg-" + randSuffix()
	containerName := "muvee-" + domainPrefix

	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"alpine", "sleep", "300",
	)

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runDescribe(ctx, task)
	if err != nil {
		t.Fatalf("runDescribe (regular project): %v", err)
	}

	summary := parseDescribeResult(t, result)
	if got := summary["container_name"]; got != containerName {
		t.Errorf("container_name = %q, want %q", got, containerName)
	}
	assertRunning(t, summary)
	t.Logf("runDescribe (regular) OK: container_name=%q", summary["container_name"])
}

// TestRunDescribe_ComposeProject is the core regression test for the bug.
// It verifies that runDescribe succeeds for a compose-project container and
// that the returned container_name is the actual compose container, not the
// nonexistent "muvee-<prefix>" name.
func TestRunDescribe_ComposeProject(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-desc-cmp-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-server-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"alpine", "sleep", "300",
	)

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runDescribe(ctx, task)
	if err != nil {
		// Before the fix this would have been:
		//   "docker inspect muvee-tst-desc-cmp-XXXX: exit status 1"
		t.Fatalf("runDescribe (compose project) FAILED — this is the bug: %v", err)
	}

	summary := parseDescribeResult(t, result)
	if got := summary["container_name"]; got != composeContainerName {
		t.Errorf("container_name = %q, want compose container %q", got, composeContainerName)
	}
	assertRunning(t, summary)
	t.Logf("runDescribe (compose) OK: container_name=%q", summary["container_name"])
}

// TestRunDescribe_StoppedContainer verifies that runDescribe succeeds for a
// stopped container (docker inspect works on exited containers).
func TestRunDescribe_StoppedContainer(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-desc-stopped-" + sfx
	containerName := "muveeproj-" + sfx + "-worker-1"

	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "echo", "done",
	)
	exec.Command("docker", "wait", containerName).Run() //nolint:errcheck

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runDescribe(ctx, task)
	if err != nil {
		t.Fatalf("runDescribe (stopped container): %v", err)
	}

	summary := parseDescribeResult(t, result)
	state, _ := summary["state"].(map[string]interface{})
	if state == nil {
		t.Fatal("missing 'state' field")
	}
	if running, _ := state["running"].(bool); running {
		t.Errorf("expected running=false for stopped container, got true")
	}
	t.Logf("runDescribe (stopped) OK: status=%q", state["status"])
}

// TestRunDescribe_NoSuchContainer verifies that runDescribe returns an error
// (mentioning docker inspect) when no container exists for the prefix.
func TestRunDescribe_NoSuchContainer(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-desc-absent-" + randSuffix()

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	_, err := runDescribe(ctx, task)
	if err == nil {
		t.Fatal("expected runDescribe to return an error for non-existent container")
	}
	if !strings.Contains(err.Error(), "docker inspect") {
		t.Errorf("expected error to mention 'docker inspect', got: %v", err)
	}
	t.Logf("runDescribe (absent) correctly returned error: %v", err)
}

// ─── runEnvInspect ───────────────────────────────────────────────────────────

// TestRunEnvInspect_RegularProject verifies that runEnvInspect returns the
// correct env vars for a regular project container.
func TestRunEnvInspect_RegularProject(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-env-reg-" + randSuffix()
	containerName := "muvee-" + domainPrefix

	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"-e", "MY_VAR=regular-value",
		"alpine", "sleep", "300",
	)

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runEnvInspect(ctx, task)
	if err != nil {
		t.Fatalf("runEnvInspect (regular project): %v", err)
	}

	envMap := parseEnvResult(t, result)
	if envMap["MY_VAR"] != "regular-value" {
		t.Errorf("MY_VAR = %q, want %q; full env: %v", envMap["MY_VAR"], "regular-value", envMap)
	}
	t.Logf("runEnvInspect (regular) OK: %d env vars", len(envMap))
}

// TestRunEnvInspect_ComposeProject verifies that runEnvInspect succeeds for a
// compose-style container and returns the correct env vars.
// Before the fix this would fail with "docker inspect muvee-<prefix>: exit status 1".
func TestRunEnvInspect_ComposeProject(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-env-cmp-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-server-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"-e", "MY_VAR=compose-value",
		"alpine", "sleep", "300",
	)

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runEnvInspect(ctx, task)
	if err != nil {
		// Before the fix: "docker inspect muvee-tst-env-cmp-XXXX: exit status 1"
		t.Fatalf("runEnvInspect (compose project) FAILED — this is the bug: %v", err)
	}

	envMap := parseEnvResult(t, result)
	if envMap["MY_VAR"] != "compose-value" {
		t.Errorf("MY_VAR = %q, want %q; full env: %v", envMap["MY_VAR"], "compose-value", envMap)
	}
	t.Logf("runEnvInspect (compose) OK: %d env vars", len(envMap))
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func parseDescribeResult(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("runDescribe returned invalid JSON: %v\nraw: %s", err, raw)
	}
	return m
}

func parseEnvResult(t *testing.T, raw string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("runEnvInspect returned invalid JSON: %v\nraw: %s", err, raw)
	}
	return m
}

func assertRunning(t *testing.T, summary map[string]interface{}) {
	t.Helper()
	state, _ := summary["state"].(map[string]interface{})
	if state == nil {
		t.Fatal("missing 'state' field in describe result")
	}
	if running, _ := state["running"].(bool); !running {
		t.Errorf("expected container to be running, state=%v", state)
	}
}
