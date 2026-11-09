//go:build integration

package deployer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCleanupComposeWithoutWorkDir reproduces the agent-recreated case: the
// compose work dir (inside the agent container) is gone, so cleanup must still
// tear the project down — containers and named volumes — by project name.
func TestCleanupComposeWithoutWorkDir(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	prefix := "zz-down-fallback-" + time.Now().Format("150405")
	project := composeProjectName(prefix)
	src := filepath.Join(t.TempDir(), "c.yml")
	yml := "services:\n  app:\n    image: alpine:3.20\n    command: [\"sleep\", \"3600\"]\n    volumes: [\"data:/data\"]\nvolumes:\n  data:\n"
	if err := os.WriteFile(src, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", src, "up", "-d").CombinedOutput(); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "compose", "-p", project, "down", "-v").Run() })

	workBase := t.TempDir() // no <projectID> dir under it: the files are lost
	var logs []string
	if err := CleanupCompose(ctx, "proj-gone", prefix, "", workBase, func(l string) { logs = append(logs, l) }); err != nil {
		t.Fatalf("CleanupCompose: %v", err)
	}

	label := "label=com.docker.compose.project=" + project
	ps, _ := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", label).Output()
	if s := strings.TrimSpace(string(ps)); s != "" {
		t.Fatalf("containers left behind: %s\nlogs:\n%s", s, strings.Join(logs, "\n"))
	}
	vols, _ := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", label).Output()
	if s := strings.TrimSpace(string(vols)); s != "" {
		t.Fatalf("volumes left behind: %s", s)
	}
}
