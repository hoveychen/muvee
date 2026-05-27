//go:build integration

package deployer

// Integration tests for deploy-time proxy isolation.
//
// Run with:
//
//	go test -v -tags=integration -count=1 ./internal/deployer/ -run TestProxy
//
// Requires docker-compose (standalone binary or docker compose plugin).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// composeCmd returns the docker-compose binary name available on this host,
// preferring the standalone "docker-compose" binary over the plugin form.
func composeCmd() (string, []string) {
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return "docker-compose", nil
	}
	// Fall back to docker compose plugin.
	return "docker", []string{"compose"}
}

// TestProxy_DefaultIsolation_NoLeakToContainer verifies the core property of
// deploy-time proxy isolation: when HTTP_PROXY is set in the agent process env
// and the user's compose file inherits it via `environment: - HTTP_PROXY`, the
// proxy variable does NOT reach the container because envForCompose strips it
// before calling docker-compose.
//
// This is the primary threat scenario: a user compose file that says
//   environment:
//     - HTTP_PROXY   # inherit from host
// would leak the deploy-agent's proxy into user containers, potentially breaking
// intra-service calls that must not go through an external proxy.
func TestProxy_DefaultIsolation_NoLeakToContainer(t *testing.T) {
	tmpdir := t.TempDir()
	composeFile := filepath.Join(tmpdir, "docker-compose.yml")
	// The compose file explicitly inherits HTTP_PROXY from the calling process —
	// simulating a user compose file that uses this common pattern.
	if err := os.WriteFile(composeFile, []byte(`services:
  probe:
    image: alpine
    environment:
      - HTTP_PROXY
      - HTTPS_PROXY
    command: ["/bin/sh", "-c", "env | grep -i proxy || true; echo __DONE__"]
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HTTP_PROXY", "http://proxy-isolation-test:9999")
	t.Setenv("HTTPS_PROXY", "http://proxy-isolation-test:9999")
	t.Setenv("DEPLOYER_PROXY_PASSTHROUGH", "") // default = disabled

	bin, prefix := composeCmd()
	args := append(prefix, "-f", composeFile, "-p", fmt.Sprintf("muvee-proxy-test-%d", os.Getpid()), "run", "--rm", "probe")

	var captured strings.Builder
	logFn := func(line string) { captured.WriteString(line) }

	if err := runCmdCompose(context.Background(), logFn, bin, args...); err != nil {
		t.Fatalf("runCmdCompose: %v\noutput: %s", err, captured.String())
	}

	output := captured.String()
	if !strings.Contains(output, "__DONE__") {
		t.Fatalf("container did not produce expected sentinel; compose may have failed\noutput: %s", output)
	}

	for _, line := range strings.Split(output, "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if proxyVarKeys[key] {
			t.Errorf("proxy var leaked into container: %q\nfull output:\n%s", line, output)
		}
	}
	t.Logf("isolation OK — no proxy vars in container env\noutput:\n%s", output)
}

// TestProxy_Passthrough_DeliverProxyToContainer verifies the opt-in passthrough
// path: when DEPLOYER_PROXY_PASSTHROUGH=true, the standard proxy vars set in
// the agent process ARE re-added to the docker-compose environment, so a user
// compose file with `environment: - HTTP_PROXY` will receive them.
func TestProxy_Passthrough_DeliverProxyToContainer(t *testing.T) {
	tmpdir := t.TempDir()
	composeFile := filepath.Join(tmpdir, "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte(`services:
  probe:
    image: alpine
    environment:
      - HTTP_PROXY
    command: ["/bin/sh", "-c", "env | grep -i proxy || true; echo __DONE__"]
`), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HTTP_PROXY", "http://proxy-passthrough-test:9999")
	t.Setenv("DEPLOYER_PROXY_PASSTHROUGH", "true")

	bin, prefix := composeCmd()
	args := append(prefix, "-f", composeFile, "-p", fmt.Sprintf("muvee-proxy-pass-%d", os.Getpid()), "run", "--rm", "probe")

	var captured strings.Builder
	logFn := func(line string) { captured.WriteString(line) }

	if err := runCmdCompose(context.Background(), logFn, bin, args...); err != nil {
		t.Fatalf("runCmdCompose: %v\noutput: %s", err, captured.String())
	}

	output := captured.String()
	if !strings.Contains(output, "__DONE__") {
		t.Fatalf("container did not produce expected sentinel\noutput: %s", output)
	}
	if !strings.Contains(output, "HTTP_PROXY=http://proxy-passthrough-test:9999") {
		t.Errorf("expected HTTP_PROXY in container env when passthrough=true\nfull output:\n%s", output)
	}
	t.Logf("passthrough OK — proxy vars present in container env\noutput:\n%s", output)
}

// TestProxy_DockerRunNoLeak confirms the baseline: `docker run` (single-container
// deploy path, uses runCmd not runCmdCompose) does NOT automatically inject
// proxy vars from the process environment into containers, so the single-container
// path requires no special isolation treatment.
func TestProxy_DockerRunNoLeak(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy-dockerrun-test:9999")
	t.Setenv("HTTPS_PROXY", "http://proxy-dockerrun-test:9999")

	out, err := exec.Command("docker", "run", "--rm", "alpine",
		"sh", "-c", "env | grep -i proxy || echo NO_PROXY_VARS").Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	result := strings.TrimSpace(string(out))

	for _, line := range strings.Split(result, "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if proxyVarKeys[key] {
			t.Errorf("docker run unexpectedly injected proxy var %q; single-container isolation assumption is broken", line)
		}
	}
	t.Logf("docker run baseline OK: %q", result)
}
