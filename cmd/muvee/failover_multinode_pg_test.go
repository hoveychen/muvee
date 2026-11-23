package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

// Real-database tests for the multi-node failover redesign (mesh-isolation P6).
// Three pre-existing problems, reproduced by the expert review on main:
//
//	(a) the replacement is dispatched to a healthy node but its deploy task
//	    fails there: failover had already stopped the old row, so the project
//	    lost its route;
//	(b) the old row was stopped by failover, so the completion callback's
//	    StopProjectDeployments found nothing and never dispatched a cleanup to
//	    the dead node — the container came back as an orphan when the node did;
//	(c) failover bypassed the deploy queue: a replacement (old image) ran
//	    concurrently with a user deploy and could silently roll it back.
//
// Needs TEST_DATABASE_URL (see failover_dispatch_fail_pg_test.go).

// builder registers an online builder node so user deploys of build-type
// projects can be dispatched.
func (f *failoverFixture) builder(hostname string) *store.Node {
	f.t.Helper()
	n, err := f.st.UpsertNode(f.ctx, &store.Node{Hostname: hostname, Role: store.NodeRoleBuilder, HostIP: "10.98.0.1"})
	if err != nil {
		f.t.Fatalf("upsert builder: %v", err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(f.ctx, `DELETE FROM nodes WHERE id = $1`, n.ID) })
	return n
}

// others returns the project's deployments other than old, newest first.
func (f *failoverFixture) others(proj *store.Project, old *store.Deployment) []*store.Deployment {
	f.t.Helper()
	deps, err := f.st.ListDeployments(f.ctx, proj.ID)
	if err != nil {
		f.t.Fatalf("list deployments: %v", err)
	}
	var out []*store.Deployment
	for _, d := range deps {
		if d.ID != old.ID {
			out = append(out, d)
		}
	}
	return out
}

// onlyReplacement asserts exactly one deployment besides old exists and returns it.
func (f *failoverFixture) onlyReplacement(proj *store.Project, old *store.Deployment) *store.Deployment {
	f.t.Helper()
	o := f.others(proj, old)
	if len(o) != 1 {
		for _, d := range o {
			f.t.Logf("  row %s status=%s node=%v logs=%q", d.ID, d.Status, d.NodeID, d.Logs)
		}
		f.t.Fatalf("project %s: %d deployment(s) besides the original, want exactly 1 replacement", proj.Name, len(o))
	}
	return o[0]
}

func (f *failoverFixture) dep(id uuid.UUID) *store.Deployment {
	f.t.Helper()
	d, err := f.st.GetDeployment(f.ctx, id)
	if err != nil || d == nil {
		f.t.Fatalf("get deployment %s: %v", id, err)
	}
	return d
}

// routes returns the project's Traefik upstreams as "ip:port".
func (f *failoverFixture) routes(proj *store.Project) []string {
	f.t.Helper()
	rs, err := f.st.GetRunningDeployments(f.ctx)
	if err != nil {
		f.t.Fatalf("running deployments: %v", err)
	}
	var out []string
	for _, r := range rs {
		if r.ProjectID == proj.ID {
			out = append(out, fmt.Sprintf("%s:%d", r.HostIP, r.HostPort))
		}
	}
	return out
}

// task returns the single task of the given type for a deployment.
func (f *failoverFixture) task(depID uuid.UUID, typ store.TaskType) *store.Task {
	f.t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx,
		`SELECT id FROM tasks WHERE deployment_id = $1 AND type = $2 ORDER BY created_at DESC LIMIT 1`, depID, string(typ)).Scan(&id); err != nil {
		f.t.Fatalf("find %s task of deployment %s: %v", typ, depID, err)
	}
	tk, err := f.st.GetTask(f.ctx, id)
	if err != nil || tk == nil {
		f.t.Fatalf("get task %s: %v", id, err)
	}
	return tk
}

func (f *failoverFixture) countTasks(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, q, args...).Scan(&n); err != nil {
		f.t.Fatalf("count tasks: %v", err)
	}
	return n
}

// completeDeployOK mirrors the API's completeTask success branch for a deploy
// task (internal/api/server.go): host port + running, retire the project's
// other running deployments (cross-node ones get a cleanup task), advance the
// deploy queue.
func (f *failoverFixture) completeDeployOK(depID uuid.UUID, hostPort int) {
	f.t.Helper()
	tk := f.task(depID, store.TaskTypeDeploy)
	if err := f.st.UpdateTaskStatus(f.ctx, tk.ID, store.TaskStatusCompleted, ""); err != nil {
		f.t.Fatalf("complete task: %v", err)
	}
	if err := f.st.SetDeploymentHostPort(f.ctx, depID, hostPort); err != nil {
		f.t.Fatalf("set host port: %v", err)
	}
	sched := scheduler.New(f.st)
	retireReplaced(f, sched, f.dep(depID), tk.NodeID)
	sched.AdvanceDeployQueueFor(f.ctx, depID)
}

// completeDeployFail mirrors the API's completeTask failure branch.
func (f *failoverFixture) completeDeployFail(depID uuid.UUID, msg string) {
	f.t.Helper()
	tk := f.task(depID, store.TaskTypeDeploy)
	if err := f.st.UpdateTaskStatus(f.ctx, tk.ID, store.TaskStatusFailed, msg); err != nil {
		f.t.Fatalf("fail task: %v", err)
	}
	if err := f.st.UpdateDeploymentStatus(f.ctx, depID, store.DeploymentStatusFailed, msg); err != nil {
		f.t.Fatalf("fail deployment: %v", err)
	}
	scheduler.New(f.st).AdvanceDeployQueueFor(f.ctx, depID)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// (a) The replacement reaches a healthy node but fails there. The original
// deployment must still be running and routed — both while the replacement is
// in flight and after it failed.
func TestFailoverMultiNode_ReplacementFailureKeepsRoute_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("fo-mn-a-dead", "10.97.1.2", false)
	healthy := f.node("fo-mn-a-healthy", "10.97.1.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-a", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 35001)

	f.failover(1)
	repl := f.onlyReplacement(proj, old)
	if repl.NodeID == nil || *repl.NodeID != healthy.ID {
		t.Fatalf("replacement node=%v, want healthy %s", repl.NodeID, healthy.ID)
	}
	if got := f.dep(old.ID).Status; got != store.DeploymentStatusRunning {
		t.Errorf("while the replacement is in flight: original status=%s, want running", got)
	}
	if r := f.routes(proj); !contains(r, "10.97.1.2:35001") {
		t.Errorf("while the replacement is in flight: routes=%v, want the original 10.97.1.2:35001 kept", r)
	}

	f.completeDeployFail(repl.ID, "pull access denied")

	if got := f.dep(old.ID); got.Status != store.DeploymentStatusRunning {
		t.Errorf("after the replacement failed: original status=%s logs=%q, want running", got.Status, got.Logs)
	}
	if r := f.routes(proj); len(r) != 1 || r[0] != "10.97.1.2:35001" {
		t.Errorf("after the replacement failed: routes=%v, want [10.97.1.2:35001] (route must not be cleared)", r)
	}
}

// (b) + the normal path end to end: replacement dispatched once, completes,
// the completion callback stops the original (with an eviction note) and
// queues a cleanup on the dead node so the orphan is removed when it returns;
// the route set ends up on the healthy node only and later ticks do nothing.
func TestFailoverMultiNode_HealthyMigrationEndToEnd_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("fo-mn-b-dead", "10.97.2.2", false)
	healthy := f.node("fo-mn-b-healthy", "10.97.2.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-b", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 35002)

	f.failover(2)
	repl := f.onlyReplacement(proj, old)
	if f.countTasks(`SELECT COUNT(*) FROM tasks WHERE deployment_id = $1 AND type = 'deploy' AND node_id = $2`, repl.ID, healthy.ID) != 1 {
		t.Fatalf("replacement does not have exactly one deploy task on the healthy node")
	}
	if dt := f.task(repl.ID, store.TaskTypeDeploy); dt.Payload["image_tag"] != old.ImageTag {
		t.Errorf("replacement deploys image %v, want the original image %s (no rebuild)", dt.Payload["image_tag"], old.ImageTag)
	}

	f.completeDeployOK(repl.ID, 36002)

	got := f.dep(old.ID)
	if got.Status != store.DeploymentStatusStopped || !strings.Contains(got.Logs, "evicted") {
		t.Errorf("original after completion: status=%s logs=%q, want stopped with an eviction note", got.Status, got.Logs)
	}
	if n := f.countTasks(`SELECT COUNT(*) FROM tasks WHERE type = 'cleanup' AND node_id = $1 AND deployment_id = $2`, dead.ID, old.ID); n != 1 {
		t.Errorf("cleanup tasks queued for the dead node: %d, want 1 (the orphan must be removed when the node returns)", n)
	}
	if r := f.routes(proj); len(r) != 1 || r[0] != "10.97.2.3:36002" {
		t.Errorf("routes=%v, want only the replacement [10.97.2.3:36002]", r)
	}

	f.failover(3)
	if o := f.others(proj, old); len(o) != 1 {
		t.Errorf("later ticks created %d extra deployment(s)", len(o)-1)
	}
}

// (c) A user deploy is in flight when the node is declared dead: failover must
// not start a concurrent replacement of the old image.
func TestFailoverMultiNode_UserDeployInFlightBlocksFailover_PG(t *testing.T) {
	f := newFailoverFixture(t)
	f.builder("fo-mn-c-builder")
	dead := f.node("fo-mn-c-dead", "10.97.3.2", false)
	f.node("fo-mn-c-healthy", "10.97.3.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-c", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
		GitURL: "https://example.invalid/r.git", GitBranch: "main",
	}, dead, 35003)

	user, err := scheduler.New(f.st).TriggerDeployment(f.ctx, proj.ID, "manual")
	if err != nil {
		t.Fatalf("trigger user deploy: %v", err)
	}
	if user.Status != store.DeploymentStatusPending {
		t.Fatalf("user deploy status=%s, want pending (dispatched)", user.Status)
	}

	f.failover(3)
	for _, d := range f.others(proj, old) {
		if d.ID != user.ID {
			t.Errorf("failover created deployment %s (status=%s image=%s) while user deploy %s was in flight",
				d.ID, d.Status, d.ImageTag, user.ID)
		}
	}
}

// (c) The converse: a replacement is in flight and the user deploys. The user
// deploy queues behind it and runs next; the project ends on the user's
// deployment, never on the old image.
func TestFailoverMultiNode_UserDeployQueuesBehindReplacement_PG(t *testing.T) {
	f := newFailoverFixture(t)
	f.builder("fo-mn-c2-builder")
	dead := f.node("fo-mn-c2-dead", "10.97.4.2", false)
	f.node("fo-mn-c2-healthy", "10.97.4.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-c2", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
		GitURL: "https://example.invalid/r.git", GitBranch: "main",
	}, dead, 35004)

	f.failover(1)
	repl := f.onlyReplacement(proj, old)
	user, err := scheduler.New(f.st).TriggerDeployment(f.ctx, proj.ID, "manual")
	if err != nil {
		t.Fatalf("trigger user deploy: %v", err)
	}
	if user.Status != store.DeploymentStatusQueued {
		t.Fatalf("user deploy status=%s, want queued behind the in-flight replacement", user.Status)
	}

	f.completeDeployOK(repl.ID, 36004)
	if got := f.dep(user.ID).Status; got != store.DeploymentStatusPending {
		t.Fatalf("after the replacement finished: user deploy status=%s, want pending (dispatched)", got)
	}
	// The user's build finishes and its deploy lands on the healthy node.
	if err := f.st.SetDeploymentImageTag(f.ctx, user.ID, "example.invalid/fo-mn-c2:new"); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.New(f.st).DispatchDeploy(f.ctx, f.dep(user.ID), proj, "example.invalid/fo-mn-c2:new"); err != nil {
		t.Fatalf("dispatch user deploy: %v", err)
	}
	f.completeDeployOK(user.ID, 36005)

	running := 0
	for _, d := range append(f.others(proj, old), f.dep(old.ID)) {
		if d.Status == store.DeploymentStatusRunning {
			running++
			if d.ID != user.ID {
				t.Errorf("deployment %s (image %s) still running; only the user's deployment should be", d.ID, d.ImageTag)
			}
		}
	}
	if running != 1 {
		t.Errorf("%d running deployment(s), want 1", running)
	}
}

// A user deployment queued at the same time as a failover replacement always
// wins, even when the replacement row happens to be newer (TriggerDeployment
// creates its row before taking the queue lock, so the commit order is not
// the created_at order).
func TestFailoverMultiNode_UserDeploySupersedesQueuedReplacement_PG(t *testing.T) {
	f := newFailoverFixture(t)
	f.builder("fo-mn-s-builder")
	dead := f.node("fo-mn-s-dead", "10.97.5.2", false)
	f.node("fo-mn-s-healthy", "10.97.5.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-s", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
		GitURL: "https://example.invalid/r.git", GitBranch: "main",
	}, dead, 35005)

	user, err := f.st.CreateDeployment(f.ctx, &store.Deployment{ProjectID: proj.ID, Status: store.DeploymentStatusQueued})
	if err != nil {
		t.Fatal(err)
	}
	repl, err := f.st.CreateDeployment(f.ctx, &store.Deployment{
		ProjectID: proj.ID, Status: store.DeploymentStatusQueued, ImageTag: old.ImageTag,
		Logs: fmt.Sprintf("failover: replacing deployment %s on offline node %s (%s)\n", old.ID, dead.Hostname, dead.ID),
	})
	if err != nil {
		t.Fatal(err)
	}

	dispatched, err := scheduler.New(f.st).AdvanceDeployQueue(f.ctx, proj.ID)
	if err != nil {
		t.Fatalf("advance queue: %v", err)
	}
	if dispatched != user.ID {
		t.Errorf("queue dispatched %s, want the user deployment %s", dispatched, user.ID)
	}
	if got := f.dep(repl.ID).Status; got != store.DeploymentStatusSuperseded {
		t.Errorf("replacement status=%s, want superseded", got)
	}
	if got := f.dep(user.ID).Status; got == store.DeploymentStatusSuperseded {
		t.Errorf("user deployment was superseded by the failover replacement")
	}
}

// The completion callback is not under the deploy-queue lock. Ticks landing
// in each window of it — replacement still deploying; replacement running but
// the original not yet stopped — must not dispatch a second replacement, and
// neither may concurrent failover passes.
func TestFailoverMultiNode_NoDuplicateReplacement_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("fo-mn-r-dead", "10.97.6.2", false)
	f.node("fo-mn-r-healthy", "10.97.6.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-r", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 35006)

	// Concurrent failover passes (overlapping ticks / replicas).
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.failover(1) }()
	}
	wg.Wait()
	repl := f.onlyReplacement(proj, old)

	// Window 1: replacement deploying.
	f.failover(2)
	f.onlyReplacement(proj, old)

	// Window 2: callback has set the replacement running but not yet retired
	// the original.
	if err := f.st.SetDeploymentHostPort(f.ctx, repl.ID, 36006); err != nil {
		t.Fatal(err)
	}
	f.failover(2)
	f.onlyReplacement(proj, old)
}

// Deterministic failure on the healthy node (e.g. the image was garbage
// collected): retries back off instead of adding a failed row every minute,
// and stop after a bounded number of attempts per outage. The original stays
// running throughout. The next outage starts counting afresh.
func TestFailoverMultiNode_DeterministicFailureBacksOff_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("fo-mn-bo-dead", "10.97.7.2", false)
	f.node("fo-mn-bo-healthy", "10.97.7.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-bo", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 35007)

	failNewest := func() {
		t.Helper()
		var newest *store.Deployment
		for _, d := range f.others(proj, old) {
			if d.Status == store.DeploymentStatusDeploying {
				newest = d
			}
		}
		if newest == nil {
			t.Fatalf("no in-flight replacement to fail")
		}
		f.completeDeployFail(newest.ID, "manifest unknown")
	}
	// age shifts every failed replacement's last update back by d, as if that
	// much time had passed since it failed.
	age := func(d string) {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx,
			`UPDATE deployments SET updated_at = updated_at - $2::interval WHERE project_id = $1 AND id <> $3 AND status = 'failed'`,
			proj.ID, d, old.ID); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() int { return len(f.others(proj, old)) }

	f.failover(1)
	failNewest()
	f.failover(30) // half an hour of one-minute ticks right after a failure
	if n := rows(); n != 1 {
		t.Fatalf("after a failure and 30 immediate ticks: %d replacement row(s), want 1 (backoff)", n)
	}
	if got := f.dep(old.ID).Status; got != store.DeploymentStatusRunning {
		t.Fatalf("original status=%s, want running", got)
	}

	// Each retry waits 5m·2^(n-1) after the n-th failure.
	waits := []string{"5 minutes", "10 minutes", "20 minutes", "40 minutes", "80 minutes", "160 minutes", "320 minutes"}
	for i, w := range waits {
		age(w + " - 30 seconds")
		f.failover(1)
		if n := rows(); n != i+1 {
			t.Fatalf("retry %d fired before its %s backoff elapsed (%d rows)", i+1, w, n)
		}
		age("31 seconds")
		f.failover(1)
		if n := rows(); n != i+2 {
			t.Fatalf("retry %d did not fire after %s (%d rows)", i+1, w, n)
		}
		failNewest()
	}
	// Eight failed attempts in this outage: give up.
	age("24 hours")
	f.failover(5)
	if n := rows(); n != 8 {
		t.Errorf("after giving up: %d replacement rows, want 8", n)
	}
	if got := f.dep(old.ID).Status; got != store.DeploymentStatusRunning {
		t.Errorf("original status=%s after giving up, want running", got)
	}

	// The node came back and died again: a new outage gets a fresh attempt.
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE deployments SET created_at = created_at - INTERVAL '3 hours' WHERE project_id = $1 AND id <> $2`, proj.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE nodes SET last_seen_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, dead.ID); err != nil {
		t.Fatal(err)
	}
	f.failover(1)
	if n := rows(); n != 9 {
		t.Errorf("next outage: %d replacement rows, want 9 (one fresh attempt)", n)
	}
}

// A compose/image project leaving a node (admin moved fixed_node_id) is torn
// down there with compose semantics — but with its named volumes kept, and in
// a mode an un-upgraded agent does not mistake for "delete the project with
// its volumes". Single-container projects keep the legacy payload.
func TestFailoverMultiNode_CrossNodeComposeCleanup_PG(t *testing.T) {
	f := newFailoverFixture(t)
	from := f.node("fo-mn-cc-from", "10.97.8.2", true)
	to := f.node("fo-mn-cc-to", "10.97.8.3", true)

	for _, tc := range []struct {
		name     string
		p        *store.Project
		wantMode string
	}{
		{"fo-mn-cc-img", &store.Project{Name: "fo-mn-cc-img", ProjectType: store.ProjectTypeImage, ImageRef: "example.invalid/app:1", ContainerPort: 8080}, "compose_stop"},
		{"fo-mn-cc-dep", &store.Project{Name: "fo-mn-cc-dep", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080}, ""},
	} {
		proj, old := f.runningProject(tc.p, from, 35008)
		if _, err := f.pool.Exec(f.ctx, `UPDATE projects SET fixed_node_id = $1 WHERE id = $2`, to.ID, proj.ID); err != nil {
			t.Fatal(err)
		}
		proj, _ = f.st.GetProject(f.ctx, proj.ID)
		nd, err := f.st.CreateDeployment(f.ctx, &store.Deployment{ProjectID: proj.ID, Status: store.DeploymentStatusDeploying, ImageTag: "example.invalid/x:2"})
		if err != nil {
			t.Fatal(err)
		}
		if err := scheduler.New(f.st).DispatchDeploy(f.ctx, nd, proj, nd.ImageTag); err != nil {
			t.Fatalf("%s: dispatch: %v", tc.name, err)
		}
		f.completeDeployOK(nd.ID, 36008)

		var payload map[string]any
		var raw string
		if err := f.pool.QueryRow(f.ctx,
			`SELECT payload FROM tasks WHERE type = 'cleanup' AND node_id = $1 AND deployment_id = $2`, from.ID, old.ID).Scan(&raw); err != nil {
			t.Fatalf("%s: cleanup task on the old node: %v", tc.name, err)
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatal(err)
		}
		mode, _ := payload["mode"].(string)
		if mode != tc.wantMode {
			t.Errorf("%s: cleanup mode=%q, want %q (payload %v)", tc.name, mode, tc.wantMode, payload)
		}
		if mode == "compose" {
			t.Errorf("%s: cleanup mode \"compose\" deletes named volumes on old and new agents", tc.name)
		}
		if tc.wantMode != "" && payload["project_id"] != proj.ID.String() {
			t.Errorf("%s: payload project_id=%v, want %s", tc.name, payload["project_id"], proj.ID)
		}
		if payload["domain_prefix"] != proj.DomainPrefix {
			t.Errorf("%s: payload domain_prefix=%v, want %s", tc.name, payload["domain_prefix"], proj.DomainPrefix)
		}
	}
}

// Single-deploy-node stacks (production io yipu/insnap, flab7 opencws): an
// agent outage with every kind of project — including one with a user deploy
// in flight and a paused one — creates no failover row, no task, and leaves
// the routes alone; when the agent returns nothing needs repairing.
func TestFailoverMultiNode_SingleNodeStackNoRows_PG(t *testing.T) {
	f := newFailoverFixture(t)
	f.builder("fo-mn-1-builder")
	only := f.node("fo-mn-1-only", "10.97.9.2", false)

	build, buildDep := f.runningProject(&store.Project{Name: "fo-mn-1-build", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080}, only, 35091)
	img, imgDep := f.runningProject(&store.Project{Name: "fo-mn-1-img", ProjectType: store.ProjectTypeImage, ContainerPort: 8080,
		ImageRef: "example.invalid/app:1", PinnedNodeID: &only.ID}, only, 35092)
	paused, pausedDep := f.runningProject(&store.Project{Name: "fo-mn-1-paused", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080}, only, 35093)
	if _, err := f.pool.Exec(f.ctx, `UPDATE projects SET paused = TRUE WHERE id = $1`, paused.ID); err != nil {
		t.Fatal(err)
	}
	busy, busyDep := f.runningProject(&store.Project{Name: "fo-mn-1-busy", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
		GitURL: "https://example.invalid/r.git", GitBranch: "main"}, only, 35094)
	user, err := scheduler.New(f.st).TriggerDeployment(f.ctx, busy.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}

	count := func() (deps, tasks int) {
		ids := []uuid.UUID{build.ID, img.ID, paused.ID, busy.ID}
		_ = f.pool.QueryRow(f.ctx, `SELECT COUNT(*) FROM deployments WHERE project_id = ANY($1)`, ids).Scan(&deps)
		_ = f.pool.QueryRow(f.ctx, `SELECT COUNT(*) FROM tasks t JOIN deployments d ON d.id = t.deployment_id WHERE d.project_id = ANY($1)`, ids).Scan(&tasks)
		return
	}
	deps0, tasks0 := count() // 4 originals + the user's deployment; its build task
	f.failover(5)
	f.setOnline(only.ID, true)
	f.failover(2)
	deps1, tasks1 := count()
	if deps1 != deps0 || tasks1 != tasks0 {
		t.Errorf("single-node outage changed rows: deployments %d→%d, tasks %d→%d", deps0, deps1, tasks0, tasks1)
	}
	for _, d := range []*store.Deployment{buildDep, imgDep, pausedDep, busyDep} {
		if got := f.dep(d.ID); got.Status != store.DeploymentStatusRunning || strings.Contains(got.Logs, "evicted") {
			t.Errorf("deployment %s: status=%s logs=%q, want untouched running", d.ID, got.Status, got.Logs)
		}
	}
	if got := f.dep(user.ID).Status; got != store.DeploymentStatusPending {
		t.Errorf("user deployment status=%s, want pending (untouched)", got)
	}
}

// Review finding: a user deploy that was dispatched to the node just before
// it died stays 'deploying' forever (its task is on the dead node, and the
// queue sweeper only visits projects with queued rows). Failover must reap it
// once the deploy queue's grace period has passed, then fail the project over.
func TestFailoverMultiNode_StuckInFlightOnDeadNodeIsReaped_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("fo-mn-st-dead", "10.97.10.2", false)
	healthy := f.node("fo-mn-st-healthy", "10.97.10.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-st", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 35010)
	stuck, err := f.st.CreateDeployment(f.ctx, &store.Deployment{ProjectID: proj.ID, NodeID: &dead.ID, Status: store.DeploymentStatusDeploying, ImageTag: "example.invalid/fo-mn-st:new"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateTask(f.ctx, &store.Task{Type: store.TaskTypeDeploy, NodeID: &dead.ID, DeploymentID: stuck.ID, Payload: map[string]any{}}); err != nil {
		t.Fatal(err)
	}

	f.failover(2) // within the queue's grace period: the stuck deploy still counts as in flight
	if o := f.others(proj, old); len(o) != 1 {
		t.Fatalf("within grace: %d deployments besides the original, want only the stuck one", len(o))
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE deployments SET updated_at = NOW() - INTERVAL '11 minutes' WHERE id = $1`, stuck.ID); err != nil {
		t.Fatal(err)
	}
	f.failover(1)
	if got := f.dep(stuck.ID).Status; got != store.DeploymentStatusFailed {
		t.Errorf("stuck deploy status=%s, want failed (reaped)", got)
	}
	var repl *store.Deployment
	for _, d := range f.others(proj, old) {
		if d.ID != stuck.ID {
			repl = d
		}
	}
	if repl == nil || repl.NodeID == nil || *repl.NodeID != healthy.ID || repl.Status != store.DeploymentStatusDeploying {
		t.Fatalf("after grace: replacement=%+v, want one deploying on the healthy node", repl)
	}
}

// Review finding: the completion callback can die between "replacement
// running" and "retire the original" (request context cancelled, DB blip).
// With nothing in flight, failover finishes that job — stop the original,
// clean up its node — instead of skipping it forever.
func TestFailoverMultiNode_FinishesInterruptedRetire_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("fo-mn-ir-dead", "10.97.11.2", false)
	healthy := f.node("fo-mn-ir-healthy", "10.97.11.3", true)
	proj, old := f.runningProject(&store.Project{
		Name: "fo-mn-ir", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 35011)
	f.failover(1)
	repl := f.onlyReplacement(proj, old)
	tk := f.task(repl.ID, store.TaskTypeDeploy)
	_ = f.st.UpdateTaskStatus(f.ctx, tk.ID, store.TaskStatusCompleted, "")
	if err := f.st.SetDeploymentHostPort(f.ctx, repl.ID, 36011); err != nil { // callback stops here
		t.Fatal(err)
	}

	f.failover(2)
	if got := f.dep(old.ID); got.Status != store.DeploymentStatusStopped || !strings.Contains(got.Logs, "evicted") {
		t.Errorf("original: status=%s logs=%q, want stopped with an eviction note", got.Status, got.Logs)
	}
	if n := f.countTasks(`SELECT COUNT(*) FROM tasks WHERE type='cleanup' AND node_id=$1 AND deployment_id=$2`, dead.ID, old.ID); n != 1 {
		t.Errorf("cleanup tasks on the dead node: %d, want exactly 1", n)
	}
	if r := f.routes(proj); len(r) != 1 || r[0] != "10.97.11.3:36011" {
		t.Errorf("routes=%v, want [10.97.11.3:36011]", r)
	}
	f.onlyReplacement(proj, old)
	_ = healthy
}

// A node row that shares its host_ip with the node the project moved to is
// the same docker host (an agent re-registered under a new NODE_NAME): the
// deploy there already replaced the container, and a cleanup would remove
// the live one if the old name ever polled again. No cleanup is queued.
func TestFailoverMultiNode_SameHostNoCleanup_PG(t *testing.T) {
	f := newFailoverFixture(t)
	oldName := f.node("fo-mn-sh-oldname", "10.97.12.2", false)
	newName := f.node("fo-mn-sh-newname", "10.97.12.2", true)
	for _, p := range []*store.Project{
		{Name: "fo-mn-sh-dep", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080},
		{Name: "fo-mn-sh-img", ProjectType: store.ProjectTypeImage, ImageRef: "example.invalid/app:1", ContainerPort: 8080},
	} {
		proj, old := f.runningProject(p, oldName, 35012)
		if _, err := f.pool.Exec(f.ctx, `UPDATE projects SET fixed_node_id = $1 WHERE id = $2`, newName.ID, proj.ID); err != nil {
			t.Fatal(err)
		}
		proj, _ = f.st.GetProject(f.ctx, proj.ID)
		nd, _ := f.st.CreateDeployment(f.ctx, &store.Deployment{ProjectID: proj.ID, Status: store.DeploymentStatusDeploying, ImageTag: "example.invalid/x:2"})
		if err := scheduler.New(f.st).DispatchDeploy(f.ctx, nd, proj, nd.ImageTag); err != nil {
			t.Fatal(err)
		}
		f.completeDeployOK(nd.ID, 36012)
		if got := f.dep(old.ID).Status; got != store.DeploymentStatusStopped {
			t.Errorf("%s: original status=%s, want stopped", p.Name, got)
		}
		if n := f.countTasks(`SELECT COUNT(*) FROM tasks WHERE type='cleanup' AND deployment_id=$1`, old.ID); n != 0 {
			t.Errorf("%s: %d cleanup task(s) queued for a node row on the same host, want 0", p.Name, n)
		}
	}
}
