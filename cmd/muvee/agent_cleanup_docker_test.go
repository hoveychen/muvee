package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hoveychen/muvee/internal/store"
)

// TestRunCleanup_ComposeStopRemovesComposeContainers_Docker runs the agent's
// cleanup against a real docker daemon. A compose/image project that moved to
// another node leaves compose-managed containers behind (muvee-<prefix>-app-1
// plus sibling services without muvee labels); the legacy cleanup only did
// `docker rm -f muvee-<prefix>` and left all of them running and holding their
// host ports. compose_stop must remove every container of the project — also
// when the compose files are gone (agent recreated) or the compose CLI is
// unavailable — without touching other containers.
//
// Containers are named mvd2-* and carry the labels a compose deploy sets.
//
//	TEST_DOCKER_CLEANUP=1 go test ./cmd/muvee/ -run TestRunCleanup_ComposeStop
func TestRunCleanup_ComposeStopRemovesComposeContainers_Docker(t *testing.T) {
	if os.Getenv("TEST_DOCKER_CLEANUP") != "1" {
		t.Skip("set TEST_DOCKER_CLEANUP=1 to run docker-backed cleanup tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	ctx := context.Background()
	suffix := strings.Split(uuid.NewString(), "-")[0]
	prefix := "mvd2-cl-" + suffix
	project := "muvee-" + prefix

	run := func(name string, labels ...string) {
		t.Helper()
		args := []string{"run", "-d", "--name", name}
		for _, l := range labels {
			args = append(args, "--label", l)
		}
		args = append(args, "busybox:latest", "sleep", "600")
		if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
			t.Fatalf("docker run %s: %v: %s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	}
	app := "mvd2-cl-" + suffix + "-app"
	db := "mvd2-cl-" + suffix + "-db"
	bystander := "mvd2-cl-" + suffix + "-other"
	run(app, "com.docker.compose.project="+project, "com.docker.compose.service=app", "muvee.domain_prefix="+prefix)
	run(db, "com.docker.compose.project="+project, "com.docker.compose.service=db")
	run(bystander, "muvee.domain_prefix=mvd2-cl-"+suffix+"x", "com.docker.compose.project=muvee-mvd2-cl-"+suffix+"x")

	err := runCleanup(ctx, &store.Task{Type: store.TaskTypeCleanup, Payload: map[string]interface{}{
		"mode":              "compose_stop",
		"project_id":        uuid.NewString(),
		"domain_prefix":     prefix,
		"compose_file_path": "docker-compose.yml",
	}})
	if err != nil {
		t.Fatalf("runCleanup: %v", err)
	}
	exists := func(name string) bool {
		out, _ := exec.Command("docker", "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.Names}}").Output()
		return strings.TrimSpace(string(out)) == name
	}
	for _, n := range []string{app, db} {
		if exists(n) {
			t.Errorf("container %s of the moved project is still there", n)
		}
	}
	if !exists(bystander) {
		t.Errorf("unrelated container %s was removed", bystander)
	}
}
