package deployer

import (
	"reflect"
	"sort"
	"testing"
)

func sortStrings(s []string) []string { sort.Strings(s); return s }

// composeForeignContainers / singleDeployForeignContainers decide which stale
// containers a deploy must tear down when a domain_prefix is reused across
// project types — the "换项目复用同域名不拆异类旧容器" gap. --remove-orphans only
// clears same-compose-project containers, so a bare `muvee-<prefix>` (or a
// compose leftover) can otherwise coexist with the new container and squat its
// port.

func TestComposeForeignContainers_RemovesBareLegacyName(t *testing.T) {
	// A deployment-type project left `muvee-fleet-relay`; the new image project's
	// own container is `muvee-fleet-relay-app-1`.
	labeled := []string{"muvee-fleet-relay", "muvee-fleet-relay-app-1"}
	got := composeForeignContainers("fleet-relay", labeled)
	want := []string{"muvee-fleet-relay"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("composeForeignContainers = %v, want %v (own compose containers must be kept)", got, want)
	}
}

func TestComposeForeignContainers_KeepsAllOwnComposeContainers(t *testing.T) {
	labeled := []string{"muvee-x-app-1", "muvee-x-worker-1"}
	if got := composeForeignContainers("x", labeled); len(got) != 0 {
		t.Errorf("composeForeignContainers = %v, want empty (all belong to the compose project)", got)
	}
}

func TestSingleDeployForeignContainers_SweepsComposeLeftovers(t *testing.T) {
	// A prior compose/image project left `muvee-x-app-1`; the new deployment-type
	// project's own container is the bare `muvee-x`.
	labeled := []string{"muvee-x", "muvee-x-app-1", "muvee-x-worker-1"}
	got := sortStrings(singleDeployForeignContainers("x", labeled))
	want := []string{"muvee-x-app-1", "muvee-x-worker-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("singleDeployForeignContainers = %v, want %v (own bare name must be kept)", got, want)
	}
}

func TestSingleDeployForeignContainers_NoLeftovers(t *testing.T) {
	if got := singleDeployForeignContainers("x", []string{"muvee-x"}); len(got) != 0 {
		t.Errorf("singleDeployForeignContainers = %v, want empty", got)
	}
}
