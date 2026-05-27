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

// ─── runRuntimeLogs ──────────────────────────────────────────────────────────

// TestRunRuntimeLogs_ComposeProject verifies that runRuntimeLogs resolves the
// container name via label for compose-style containers.
// Before the fix this produced "No such container" output even for running
// containers because the name "muvee-<prefix>" doesn't exist for compose projects.
func TestRunRuntimeLogs_ComposeProject(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-logs-cmp-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-server-1"

	// Write a known log line, then keep the container alive.
	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"alpine", "sh", "-c", "echo hello-from-compose && sleep 300",
	)

	task := &store.Task{Payload: map[string]interface{}{
		"domain_prefix": domainPrefix,
		"tail":          float64(50),
	}}
	result, err := runRuntimeLogs(ctx, task)
	if err != nil {
		t.Fatalf("runRuntimeLogs (compose project) FAILED — this is the bug: %v", err)
	}
	if strings.Contains(result, "No such container") {
		t.Errorf("runRuntimeLogs returned 'No such container' for a running compose container: %s", result)
	}
	if !strings.Contains(result, "hello-from-compose") {
		t.Errorf("expected log output to contain 'hello-from-compose', got: %s", result)
	}
	t.Logf("runRuntimeLogs (compose) OK: %d bytes", len(result))
}

// ─── runRestart ──────────────────────────────────────────────────────────────

// TestRunRestart_ComposeProject verifies that runRestart resolves the container
// name via label for compose-style containers and actually restarts it.
// Before the fix, restart silently did nothing (the "No such container" branch
// returned success with the docker error message as body).
//
// NOTE: docker restart does NOT increment RestartCount (that counter tracks
// automatic restarts triggered by the restart policy, not manual docker restart
// calls). We use State.StartedAt as the restart witness instead — it always
// advances after a successful docker restart.
func TestRunRestart_ComposeProject(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-restart-cmp-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-server-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"alpine", "sleep", "300",
	)

	// Capture State.StartedAt before restart. docker restart always updates it,
	// even when the container was already running.
	beforeOut, err := exec.Command("docker", "inspect",
		"--format", "{{.State.StartedAt}}", composeContainerName).Output()
	if err != nil {
		t.Fatalf("docker inspect before restart: %v", err)
	}
	beforeStartedAt := strings.TrimSpace(string(beforeOut))

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, taskErr := runRestart(ctx, task)
	if taskErr != nil {
		t.Fatalf("runRestart (compose project) FAILED: %v", taskErr)
	}
	if strings.Contains(result, "No such container") {
		t.Errorf("runRestart returned 'No such container' for a running compose container — label lookup not working: %s", result)
	}

	afterOut, err := exec.Command("docker", "inspect",
		"--format", "{{.State.StartedAt}}", composeContainerName).Output()
	if err != nil {
		t.Fatalf("docker inspect after restart: %v", err)
	}
	afterStartedAt := strings.TrimSpace(string(afterOut))
	if beforeStartedAt == afterStartedAt {
		t.Errorf("State.StartedAt unchanged (%s); docker restart did not target the correct container", beforeStartedAt)
	}
	t.Logf("runRestart (compose) OK: StartedAt %s → %s, result=%q", beforeStartedAt, afterStartedAt, result)
}

// ─── runRuntimeLogs (additional) ─────────────────────────────────────────────

// TestRunRuntimeLogs_RegularProject verifies that runRuntimeLogs works for a
// regular (non-compose) project whose container is named "muvee-<prefix>".
func TestRunRuntimeLogs_RegularProject(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-logs-reg-" + randSuffix()
	containerName := "muvee-" + domainPrefix

	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "sh", "-c", "echo regular-log-line && sleep 300",
	)

	task := &store.Task{Payload: map[string]interface{}{
		"domain_prefix": domainPrefix,
		"tail":          float64(10),
	}}
	result, err := runRuntimeLogs(ctx, task)
	if err != nil {
		t.Fatalf("runRuntimeLogs (regular): %v", err)
	}
	if !strings.Contains(result, "regular-log-line") {
		t.Errorf("expected 'regular-log-line' in output, got: %s", result)
	}
	t.Logf("runRuntimeLogs (regular) OK: %d bytes", len(result))
}

// TestRunRuntimeLogs_StoppedComposeContainer verifies that docker logs still
// works after a container has exited — logs are persisted regardless of state.
func TestRunRuntimeLogs_StoppedComposeContainer(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-logs-stopped-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-worker-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "sh", "-c", "echo stopped-container-log && exit 0",
	)
	// Wait for container to exit.
	exec.Command("docker", "wait", composeContainerName).Run() //nolint:errcheck

	task := &store.Task{Payload: map[string]interface{}{
		"domain_prefix": domainPrefix,
		"tail":          float64(20),
	}}
	result, err := runRuntimeLogs(ctx, task)
	if err != nil {
		t.Fatalf("runRuntimeLogs (stopped compose): %v", err)
	}
	if strings.Contains(result, "No such container") {
		t.Errorf("stopped container should not return 'No such container': %s", result)
	}
	if !strings.Contains(result, "stopped-container-log") {
		t.Errorf("expected 'stopped-container-log' in output, got: %s", result)
	}
	t.Logf("runRuntimeLogs (stopped compose) OK: %d bytes", len(result))
}

// TestRunRuntimeLogs_NoSuchContainer verifies the soft-success path: when no
// container exists for the prefix, "No such container" is returned as the body
// with no error — the CLI should print it rather than showing a task failure.
func TestRunRuntimeLogs_NoSuchContainer(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-logs-absent-" + randSuffix()

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runRuntimeLogs(ctx, task)
	if err != nil {
		t.Fatalf("runRuntimeLogs (absent) must not return error, got: %v", err)
	}
	if !strings.Contains(result, "No such container") {
		t.Errorf("expected 'No such container' in result body, got: %q", result)
	}
	t.Logf("runRuntimeLogs (absent) soft-success OK: %q", result)
}

// ─── runRestart (additional) ──────────────────────────────────────────────────

// TestRunRestart_RegularProject verifies that runRestart works for a regular
// (non-compose) project container named "muvee-<prefix>".
func TestRunRestart_RegularProject(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-restart-reg-" + randSuffix()
	containerName := "muvee-" + domainPrefix

	dockerRunDetached(t, containerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "sleep", "300",
	)

	beforeOut, err := exec.Command("docker", "inspect",
		"--format", "{{.State.StartedAt}}", containerName).Output()
	if err != nil {
		t.Fatalf("docker inspect before restart: %v", err)
	}
	beforeStartedAt := strings.TrimSpace(string(beforeOut))

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, taskErr := runRestart(ctx, task)
	if taskErr != nil {
		t.Fatalf("runRestart (regular): %v", taskErr)
	}
	if strings.Contains(result, "No such container") {
		t.Errorf("runRestart returned 'No such container' for a running regular container: %s", result)
	}

	afterOut, err := exec.Command("docker", "inspect",
		"--format", "{{.State.StartedAt}}", containerName).Output()
	if err != nil {
		t.Fatalf("docker inspect after restart: %v", err)
	}
	afterStartedAt := strings.TrimSpace(string(afterOut))
	if beforeStartedAt == afterStartedAt {
		t.Errorf("State.StartedAt unchanged after restart: %s", beforeStartedAt)
	}
	t.Logf("runRestart (regular) OK: StartedAt %s → %s", beforeStartedAt, afterStartedAt)
}

// TestRunRestart_StoppedComposeContainer verifies that runRestart starts a
// stopped container and that State.StartedAt advances.
func TestRunRestart_StoppedComposeContainer(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-restart-stopped-" + sfx
	composeContainerName := "muveeproj-" + sfx + "-app-1"

	dockerRunDetached(t, composeContainerName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "echo", "done",
	)
	exec.Command("docker", "wait", composeContainerName).Run() //nolint:errcheck

	beforeOut, _ := exec.Command("docker", "inspect",
		"--format", "{{.State.StartedAt}}", composeContainerName).Output()
	beforeStartedAt := strings.TrimSpace(string(beforeOut))

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, taskErr := runRestart(ctx, task)
	if taskErr != nil {
		t.Fatalf("runRestart (stopped compose): %v", taskErr)
	}
	if strings.Contains(result, "No such container") {
		t.Errorf("runRestart returned 'No such container' for a stopped compose container: %s", result)
	}

	afterOut, err := exec.Command("docker", "inspect",
		"--format", "{{.State.StartedAt}}", composeContainerName).Output()
	if err != nil {
		t.Fatalf("docker inspect after restart: %v", err)
	}
	afterStartedAt := strings.TrimSpace(string(afterOut))
	if beforeStartedAt == afterStartedAt {
		t.Errorf("State.StartedAt unchanged; restart may not have succeeded: %s", beforeStartedAt)
	}
	t.Logf("runRestart (stopped compose) OK: StartedAt %s → %s", beforeStartedAt, afterStartedAt)
}

// TestRunRestart_NoSuchContainer verifies the soft-success path: a restart for
// a non-existent prefix returns a body containing "No such container" but no
// error, matching the soft-success contract of runRuntimeLogs.
func TestRunRestart_NoSuchContainer(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-restart-absent-" + randSuffix()

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	result, err := runRestart(ctx, task)
	if err != nil {
		t.Fatalf("runRestart (absent) must not return error, got: %v", err)
	}
	if !strings.Contains(result, "No such container") {
		t.Errorf("expected 'No such container' in result body, got: %q", result)
	}
	t.Logf("runRestart (absent) soft-success OK: %q", result)
}

// ─── resolveContainerName (multi-match) ──────────────────────────────────────

// TestResolveContainerName_MultiMatch verifies the newest-first heuristic:
// when a stale stopped container and a newer running container both carry the
// same muvee.domain_prefix label, resolveContainerName returns the newer one.
// This mirrors the real scenario where a previous compose deploy was not fully
// cleaned up and a fresh deploy created a new container.
func TestResolveContainerName_MultiMatch(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-multi-" + sfx
	staleName := "muveeproj-" + sfx + "-old-1"
	newName := "muveeproj-" + sfx + "-new-1"

	// Create the "stale" container first, then stop it.
	dockerRunDetached(t, staleName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "echo", "stale",
	)
	exec.Command("docker", "wait", staleName).Run() //nolint:errcheck

	// Create the "new" container after — this must be returned.
	dockerRunDetached(t, newName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"alpine", "sleep", "300",
	)

	got := resolveContainerName(ctx, domainPrefix)
	if got != newName {
		t.Errorf("resolveContainerName(%q) = %q, want newest %q (stale=%q)",
			domainPrefix, got, newName, staleName)
	}
	t.Logf("resolveContainerName (multi-match) OK: returned %q over stale %q", newName, staleName)
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
