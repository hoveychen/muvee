package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

// planDeleteCleanup is the decision behind the project-delete container teardown.
// The regression it guards: a `deployment`-type project used to fall through the
// cleanup guard entirely, orphaning its `muvee-<prefix>` container so it kept
// squatting its host port after the project was gone.

func ptrNode(id uuid.UUID) *uuid.UUID { return &id }

func TestPlanDeleteCleanup_DeploymentType_UsesLatestDeploymentNode(t *testing.T) {
	node := uuid.New()
	newest := uuid.New()
	older := uuid.New()
	proj := &store.Project{ProjectType: store.ProjectTypeDeployment, DomainPrefix: "x"}
	// ListDeployments returns newest-first; the current container lives on the
	// node of the most recent deployment that recorded one.
	deps := []*store.Deployment{
		{ID: newest, NodeID: ptrNode(node)},
		{ID: older, NodeID: ptrNode(uuid.New())},
	}
	plan, ok := planDeleteCleanup(proj, deps)
	if !ok {
		t.Fatal("expected a cleanup plan for a deployment-type project with a deployed node")
	}
	if plan.mode != "single" {
		t.Errorf("mode = %q, want single", plan.mode)
	}
	if plan.nodeID != node {
		t.Errorf("nodeID = %s, want %s (newest deployment's node)", plan.nodeID, node)
	}
	if plan.deployID != newest {
		t.Errorf("deployID = %s, want %s (newest deployment)", plan.deployID, newest)
	}
}

func TestPlanDeleteCleanup_DeploymentType_SkipsWhenNoNode(t *testing.T) {
	proj := &store.Project{ProjectType: store.ProjectTypeDeployment, DomainPrefix: "x"}
	if _, ok := planDeleteCleanup(proj, nil); ok {
		t.Fatal("expected no cleanup plan when the project was never deployed")
	}
	// A deployment row with no node_id gives us nowhere to send the cleanup.
	deps := []*store.Deployment{{ID: uuid.New(), NodeID: nil}}
	if _, ok := planDeleteCleanup(proj, deps); ok {
		t.Fatal("expected no cleanup plan when no deployment recorded a node")
	}
}

func TestPlanDeleteCleanup_ComposeAndImage_UseComposeMode(t *testing.T) {
	pinned := uuid.New()
	dep := uuid.New()
	deps := []*store.Deployment{{ID: dep, NodeID: ptrNode(uuid.New())}}
	for _, pt := range []store.ProjectType{store.ProjectTypeCompose, store.ProjectTypeImage} {
		proj := &store.Project{ProjectType: pt, DomainPrefix: "x", PinnedNodeID: ptrNode(pinned)}
		plan, ok := planDeleteCleanup(proj, deps)
		if !ok {
			t.Fatalf("%s: expected a cleanup plan", pt)
		}
		if plan.mode != "compose" {
			t.Errorf("%s: mode = %q, want compose", pt, plan.mode)
		}
		if plan.nodeID != pinned {
			t.Errorf("%s: nodeID = %s, want pinned %s", pt, plan.nodeID, pinned)
		}
		if plan.deployID != dep {
			t.Errorf("%s: deployID = %s, want %s", pt, plan.deployID, dep)
		}
	}
}

func TestPlanDeleteCleanup_SkipsWhenNothingToDo(t *testing.T) {
	// nil project
	if _, ok := planDeleteCleanup(nil, nil); ok {
		t.Fatal("nil project should not produce a cleanup plan")
	}
	// compose without a pinned node was never deployed
	proj := &store.Project{ProjectType: store.ProjectTypeCompose, DomainPrefix: "x"}
	if _, ok := planDeleteCleanup(proj, []*store.Deployment{{ID: uuid.New()}}); ok {
		t.Fatal("compose without PinnedNodeID should not produce a cleanup plan")
	}
	// domain-only reservations have no container
	do := &store.Project{ProjectType: store.ProjectTypeDomainOnly, DomainPrefix: "x"}
	if _, ok := planDeleteCleanup(do, nil); ok {
		t.Fatal("domain-only project should not produce a cleanup plan")
	}
}
