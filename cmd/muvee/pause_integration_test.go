//go:build integration

package main

// Integration tests for the pause/unpause container-selection leaks.
//
// Run with:
//
//	go test -v -tags=integration -count=1 ./cmd/muvee/ -run TestRunPause
//	go test -v -tags=integration -count=1 ./cmd/muvee/ -run TestRunUnpause
//
// Requires Docker daemon accessible to the current user.

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

// containerRunning reports the .State.Running flag for a container.
func containerRunning(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("docker", "inspect",
		"--format", "{{.State.Running}}", name).Output()
	if err != nil {
		t.Fatalf("docker inspect %s: %v", name, err)
	}
	return strings.TrimSpace(string(out)) == "true"
}

// TestRunPause_ComposeSiblingContainers reproduces the compose leak: the
// muvee.override.yml only stamps the expose service with the
// muvee.domain_prefix label, so sibling services (db, cache, workers) carry
// only the com.docker.compose.project label. Pause must stop them too —
// otherwise "paused" projects keep burning CPU/memory on every non-expose
// service.
func TestRunPause_ComposeSiblingContainers(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-pause-cmp-" + sfx
	// Deploys use composeProjectName(prefix) == "muvee-<prefix>" as the
	// compose project name (docker compose -p).
	composeProject := "muvee-" + domainPrefix
	exposeName := composeProject + "-api-1"
	siblingName := composeProject + "-db-1"

	// Expose service: carries both labels, exactly as buildComposeOverrideYAML
	// stamps it plus what docker compose adds on its own.
	dockerRunDetached(t, exposeName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"--label", "com.docker.compose.project="+composeProject,
		"alpine", "sleep", "300",
	)
	// Sibling service: only the compose-project label, no muvee labels.
	dockerRunDetached(t, siblingName,
		"--label", "com.docker.compose.project="+composeProject,
		"alpine", "sleep", "300",
	)

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	if _, err := runPause(ctx, task); err != nil {
		t.Fatalf("runPause: %v", err)
	}

	if containerRunning(t, exposeName) {
		t.Errorf("expose container %s still running after pause", exposeName)
	}
	if containerRunning(t, siblingName) {
		t.Errorf("sibling container %s still running after pause — compose leak", siblingName)
	}
}

// TestRunPause_LegacyUnlabeledContainer reproduces the legacy leak: containers
// deployed before the muvee.domain_prefix label was introduced are named
// "muvee-<prefix>" but carry no labels, so the label-filtered docker ps finds
// nothing and runPause reports success while the container keeps running.
func TestRunPause_LegacyUnlabeledContainer(t *testing.T) {
	ctx := context.Background()
	domainPrefix := "tst-pause-old-" + randSuffix()
	containerName := "muvee-" + domainPrefix

	dockerRunDetached(t, containerName,
		"alpine", "sleep", "300",
	)

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	if _, err := runPause(ctx, task); err != nil {
		t.Fatalf("runPause: %v", err)
	}

	if containerRunning(t, containerName) {
		t.Errorf("legacy unlabeled container %s still running after pause", containerName)
	}
}

// TestRunUnpause_ComposeSiblingContainers is the resume counterpart of the
// compose leak: after a (correct) pause stopped every service, unpause must
// docker-start the sibling services too, not just the labeled expose service.
func TestRunUnpause_ComposeSiblingContainers(t *testing.T) {
	ctx := context.Background()
	sfx := randSuffix()
	domainPrefix := "tst-unpause-cmp-" + sfx
	composeProject := "muvee-" + domainPrefix
	exposeName := composeProject + "-api-1"
	siblingName := composeProject + "-db-1"

	dockerRunDetached(t, exposeName,
		"--label", "muvee.domain_prefix="+domainPrefix,
		"--label", "muvee.expose_port=8080",
		"--label", "com.docker.compose.project="+composeProject,
		"alpine", "sleep", "300",
	)
	dockerRunDetached(t, siblingName,
		"--label", "com.docker.compose.project="+composeProject,
		"alpine", "sleep", "300",
	)
	// Put both into the paused state (stopped) out-of-band.
	for _, name := range []string{exposeName, siblingName} {
		if out, err := exec.Command("docker", "stop", name).CombinedOutput(); err != nil {
			t.Fatalf("docker stop %s: %v\n%s", name, err, out)
		}
	}

	task := &store.Task{Payload: map[string]interface{}{"domain_prefix": domainPrefix}}
	if _, err := runUnpause(ctx, task); err != nil {
		t.Fatalf("runUnpause: %v", err)
	}

	if !containerRunning(t, exposeName) {
		t.Errorf("expose container %s not running after unpause", exposeName)
	}
	if !containerRunning(t, siblingName) {
		t.Errorf("sibling container %s not running after unpause — compose leak", siblingName)
	}
}
