package deployer

import (
	"context"
	"os/exec"
	"strings"
)

// composeForeignContainers returns the containers a compose/image deploy of
// domainPrefix must remove before `docker compose up`. A compose deploy owns
// containers namespaced under the compose project `muvee-<prefix>-` (e.g.
// muvee-<prefix>-app-1); any other container carrying the
// muvee.domain_prefix=<prefix> label is foreign — most commonly the legacy bare
// `muvee-<prefix>` container left behind by a deleted deployment-type project
// that reused this domain. `docker compose up --remove-orphans` does NOT remove
// it, because it lacks the `com.docker.compose.project` label.
func composeForeignContainers(domainPrefix string, labeled []string) []string {
	own := "muvee-" + domainPrefix + "-"
	var out []string
	for _, n := range labeled {
		if n == "" || strings.HasPrefix(n, own) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// singleDeployForeignContainers returns the containers a single-container
// (deployment-type) deploy of domainPrefix must remove. The deploy already
// removes its own exact `muvee-<prefix>` name; this additionally sweeps
// compose-style leftovers (muvee-<prefix>-<service>-N) from a prior
// compose/image project that reused this domain.
func singleDeployForeignContainers(domainPrefix string, labeled []string) []string {
	self := "muvee-" + domainPrefix
	var out []string
	for _, n := range labeled {
		if n == "" || n == self {
			continue
		}
		out = append(out, n)
	}
	return out
}

// listContainersByDomainPrefix returns the names of every container (running or
// stopped) carrying the muvee.domain_prefix=<prefix> label on this node. Best
// effort: returns nil on any docker error.
func listContainersByDomainPrefix(ctx context.Context, domainPrefix string) []string {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=muvee.domain_prefix="+domainPrefix,
		"--format", "{{.Names}}").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, n := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}
