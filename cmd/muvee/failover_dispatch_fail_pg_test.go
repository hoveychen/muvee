package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

// Real-database regression tests for BACKLOG-20261124-002: node failover used
// to mark the dead node's running deployments 'stopped' *before* dispatching a
// replacement. When the replacement could not be dispatched (single deploy
// node → "no active deploy nodes"; fixed node offline; compose/image project
// pinned to the dead node), the replacement was marked failed and the old row
// stayed stopped — Traefik dropped the route although the container was still
// running, and nothing put it back once the agent returned. Production insnap
// sub-sites were 404 for 15 days until the rows were fixed by hand.
//
// Needs TEST_DATABASE_URL, e.g.:
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./cmd/muvee/ -run TestCheckNodeFailovers_
type failoverFixture struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	st    *store.Store
	owner *store.User
}

func newFailoverFixture(t *testing.T) *failoverFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(pool)
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email) VALUES ('failover-dispatch-test@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := st.GetUserByEmail(ctx, "failover-dispatch-test@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	return &failoverFixture{t: t, ctx: ctx, pool: pool, st: st, owner: owner}
}

// node creates a deploy node; offline nodes were last seen an hour ago.
func (f *failoverFixture) node(hostname, ip string, online bool) *store.Node {
	f.t.Helper()
	n, err := f.st.UpsertNode(f.ctx, &store.Node{Hostname: hostname, Role: store.NodeRoleDeploy, HostIP: ip})
	if err != nil {
		f.t.Fatalf("upsert node: %v", err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, n.ID) })
	if !online {
		f.setOnline(n.ID, false)
	}
	return n
}

func (f *failoverFixture) setOnline(nodeID uuid.UUID, online bool) {
	f.t.Helper()
	q := `UPDATE nodes SET last_seen_at = NOW() WHERE id = $1`
	if !online {
		q = `UPDATE nodes SET last_seen_at = NOW() - INTERVAL '1 hour' WHERE id = $1`
	}
	if _, err := f.pool.Exec(f.ctx, q, nodeID); err != nil {
		f.t.Fatalf("set node liveness: %v", err)
	}
}

// runningProject creates a project with one running deployment on node.
func (f *failoverFixture) runningProject(p *store.Project, node *store.Node, hostPort int) (*store.Project, *store.Deployment) {
	f.t.Helper()
	p.OwnerID = f.owner.ID
	p.DomainPrefix = p.Name
	proj, err := f.st.CreateProject(f.ctx, p)
	if err != nil {
		f.t.Fatalf("create project %s: %v", p.Name, err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, proj.ID) })
	dep, err := f.st.CreateDeployment(f.ctx, &store.Deployment{
		ProjectID: proj.ID,
		NodeID:    &node.ID,
		ImageTag:  "example.invalid/" + p.Name + ":sha-deadbeef",
	})
	if err != nil {
		f.t.Fatalf("create deployment: %v", err)
	}
	if err := f.st.SetDeploymentHostPort(f.ctx, dep.ID, hostPort); err != nil {
		f.t.Fatalf("set host port: %v", err)
	}
	return proj, dep
}

func (f *failoverFixture) failover(times int) {
	sched := scheduler.New(f.st)
	for i := 0; i < times; i++ {
		checkNodeFailovers(f.ctx, f.st, sched, 3*time.Minute)
	}
}

// assertUntouched: the project still has exactly its original deployment, on
// 'running', with no eviction note, no task, and it is still routed.
func (f *failoverFixture) assertUntouched(proj *store.Project, dep *store.Deployment) {
	f.t.Helper()
	deps, err := f.st.ListDeployments(f.ctx, proj.ID)
	if err != nil {
		f.t.Fatalf("list deployments: %v", err)
	}
	for _, d := range deps {
		if d.ID != dep.ID {
			f.t.Errorf("project %s: leftover replacement deployment %s status=%s logs=%q", proj.Name, d.ID, d.Status, d.Logs)
		}
	}
	got, err := f.st.GetDeployment(f.ctx, dep.ID)
	if err != nil || got == nil {
		f.t.Fatalf("project %s: original deployment gone: %v", proj.Name, err)
	}
	if got.Status != store.DeploymentStatusRunning {
		f.t.Errorf("project %s: original deployment status=%s, want running (logs=%q)", proj.Name, got.Status, got.Logs)
	}
	if strings.Contains(got.Logs, "evicted") {
		f.t.Errorf("project %s: original deployment carries an eviction note although it was not evicted: %q", proj.Name, got.Logs)
	}
	var tasks int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM tasks t JOIN deployments d ON d.id = t.deployment_id WHERE d.project_id = $1`, proj.ID).Scan(&tasks); err != nil {
		f.t.Fatalf("count tasks: %v", err)
	}
	if tasks != 0 {
		f.t.Errorf("project %s: %d task(s) created, want 0", proj.Name, tasks)
	}
	routes, err := f.st.GetRunningDeployments(f.ctx)
	if err != nil {
		f.t.Fatalf("running deployments: %v", err)
	}
	for _, r := range routes {
		if r.DeploymentID == dep.ID {
			return
		}
	}
	f.t.Errorf("project %s: deployment %s missing from the Traefik route set", proj.Name, dep.ID)
}

// The production shape: the only deploy node's agent is gone, containers keep
// running. Neither a build-type project (no other node to pick) nor an image
// project (pinned to the dead node) may lose its route, repeated ticks must not
// pile up rows, and once the agent is back nothing needs fixing by hand.
func TestCheckNodeFailovers_SingleNodeKeepsRunning_PG(t *testing.T) {
	f := newFailoverFixture(t)
	only := f.node("failover-dispatch-only", "10.99.1.2", false)

	build, buildDep := f.runningProject(&store.Project{
		Name: "fo-single-build", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, only, 34901)
	img, imgDep := f.runningProject(&store.Project{
		Name: "fo-single-image", ProjectType: store.ProjectTypeImage, ContainerPort: 8080,
		ImageRef: "example.invalid/app:latest", PinnedNodeID: &only.ID,
	}, only, 34902)

	f.failover(3) // three ticks of a long outage
	f.assertUntouched(build, buildDep)
	f.assertUntouched(img, imgDep)

	// Agent comes back: no manual DB repair required.
	f.setOnline(only.ID, true)
	f.failover(1)
	f.assertUntouched(build, buildDep)
	f.assertUntouched(img, imgDep)
}

// A project fixed (admin fixed_node_id) to the dead node cannot move even when
// another healthy deploy node exists.
func TestCheckNodeFailovers_FixedNodeOfflineKeepsRunning_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("failover-dispatch-fixed-dead", "10.99.2.2", false)
	f.node("failover-dispatch-fixed-healthy", "10.99.2.3", true)

	proj, dep := f.runningProject(&store.Project{
		Name: "fo-fixed", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 34903)
	if _, err := f.pool.Exec(f.ctx, `UPDATE projects SET fixed_node_id = $1 WHERE id = $2`, dead.ID, proj.ID); err != nil {
		t.Fatalf("fix node: %v", err)
	}

	f.failover(3)
	f.assertUntouched(proj, dep)
}

// Dispatch fails for a reason failover cannot foresee (here: the project is
// fixed to a *different* node that is offline too). The replacement row must be
// rolled back and the original deployment must never have been stopped.
func TestCheckNodeFailovers_DispatchFailureRollsBack_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("failover-dispatch-rb-dead", "10.99.3.2", false)
	otherDead := f.node("failover-dispatch-rb-other", "10.99.3.4", false)
	f.node("failover-dispatch-rb-healthy", "10.99.3.3", true)

	proj, dep := f.runningProject(&store.Project{
		Name: "fo-rollback", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 34904)
	if _, err := f.pool.Exec(f.ctx, `UPDATE projects SET fixed_node_id = $1 WHERE id = $2`, otherDead.ID, proj.ID); err != nil {
		t.Fatalf("fix node: %v", err)
	}

	f.failover(3)
	f.assertUntouched(proj, dep)
}

// Regression guard for the normal case: with a healthy node available a
// replacement of the dead node's deployment is dispatched there — once. The
// original stays running and routed until the replacement completes (P6: the
// completion callback retires it, see failover_multinode_pg_test.go).
func TestCheckNodeFailovers_HealthyNodeStillMigrates_PG(t *testing.T) {
	f := newFailoverFixture(t)
	dead := f.node("failover-dispatch-mig-dead", "10.99.4.2", false)
	healthy := f.node("failover-dispatch-mig-healthy", "10.99.4.3", true)

	proj, dep := f.runningProject(&store.Project{
		Name: "fo-migrate", ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080,
	}, dead, 34905)

	f.failover(2) // the second tick must not evict again

	old, err := f.st.GetDeployment(f.ctx, dep.ID)
	if err != nil || old == nil {
		t.Fatalf("get old deployment: %v", err)
	}
	if old.Status != store.DeploymentStatusRunning || strings.Contains(old.Logs, "evicted") {
		t.Errorf("old deployment status=%s logs=%q, want still running (retired only when the replacement completes)", old.Status, old.Logs)
	}
	deps, err := f.st.ListDeployments(f.ctx, proj.ID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %d deployments, want original + exactly one replacement", len(deps))
	}
	repl := deps[0]
	if repl.ID == dep.ID {
		repl = deps[1]
	}
	if repl.Status != store.DeploymentStatusDeploying {
		t.Errorf("replacement status=%s, want deploying (logs=%q)", repl.Status, repl.Logs)
	}
	if repl.NodeID == nil || *repl.NodeID != healthy.ID {
		t.Errorf("replacement node=%v, want healthy node %s", repl.NodeID, healthy.ID)
	}
	var tasks int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM tasks WHERE deployment_id = $1 AND type = 'deploy' AND node_id = $2`, repl.ID, healthy.ID).Scan(&tasks); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if tasks != 1 {
		t.Errorf("replacement has %d deploy task(s) on the healthy node, want 1", tasks)
	}
}
