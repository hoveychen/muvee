package scheduler

import (
	"strings"
	"testing"
)

// buildInlineComposeYAML synthesizes the compose file for image-type projects.
// Bound project secrets are written by the agent to workDir/.env; the service
// must reference that file via env_file so the values reach the container as
// real environment variables (mirroring the deployment-type `docker run -e`
// path). Without it, image projects silently run with no secrets injected.
func TestBuildInlineComposeYAMLReferencesEnvFile(t *testing.T) {
	out := buildInlineComposeYAML("ghcr.io/owner/app:latest", 8080)

	if !strings.Contains(out, "image: ghcr.io/owner/app:latest") {
		t.Fatalf("expected image line in compose, got:\n%s", out)
	}
	if !strings.Contains(out, "env_file:") {
		t.Fatalf("expected env_file directive so bound secrets reach the container, got:\n%s", out)
	}
	if !strings.Contains(out, ".env") {
		t.Fatalf("expected env_file to reference .env, got:\n%s", out)
	}
}
