package scheduler

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real-database tests for the per-project deploy queue (serial execution,
// newest-queued-wins, lost-deployment recovery). They only run when
// TEST_DATABASE_URL points at a disposable Postgres, e.g.:
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/scheduler/ -run DeployQueue

type queueEnv struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	st      *store.Store
	sched   *Scheduler
	node    uuid.UUID
	builder uuid.UUID
	owner   uuid.UUID
}

func newQueueEnv(t *testing.T) *queueEnv {
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
	e := &queueEnv{t: t, ctx: ctx, pool: pool, st: st, sched: New(st)}

	suffix := uuid.NewString()[:8]
	deployNode, err := st.UpsertNode(ctx, &store.Node{Hostname: "queue-deploy-" + suffix, Role: store.NodeRoleDeploy, HostIP: "10.0.0.1"})
	if err != nil {
		t.Fatalf("upsert deploy node: %v", err)
	}
	builderNode, err := st.UpsertNode(ctx, &store.Node{Hostname: "queue-builder-" + suffix, Role: store.NodeRoleBuilder, HostIP: "10.0.0.2"})
	if err != nil {
		t.Fatalf("upsert builder node: %v", err)
	}
	e.node, e.builder = deployNode.ID, builderNode.ID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "queue-"+suffix+"@example.com").Scan(&e.owner); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		// Other deploy nodes left behind would steal picks in later tests.
		pool.Exec(ctx, `DELETE FROM nodes WHERE id = ANY($1)`, []uuid.UUID{e.node, e.builder})
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, e.owner)
	})
	return e
}

func (e *queueEnv) project(pt store.ProjectType) *store.Project {
	e.t.Helper()
	name := "queue-" + uuid.NewString()[:8]
	p := &store.Project{
		Name:          name,
		ProjectType:   pt,
		DomainPrefix:  name,
		OwnerID:       e.owner,
		ContainerPort: 8080,
	}
	switch pt {
	case store.ProjectTypeImage:
		p.ImageRef = "nginx:alpine"
	case store.ProjectTypeDeployment:
		p.GitURL = "https://example.com/repo.git"
		p.GitBranch = "main"
	}
	created, err := e.st.CreateProject(e.ctx, p)
	if err != nil {
		e.t.Fatalf("create project: %v", err)
	}
	e.t.Cleanup(func() { e.pool.Exec(e.ctx, `DELETE FROM projects WHERE id = $1`, created.ID) })
	return created
}

func (e *queueEnv) trigger(p *store.Project) *store.Deployment {
	e.t.Helper()
	d, err := e.sched.TriggerDeployment(e.ctx, p.ID, "test")
	if err != nil {
		e.t.Fatalf("trigger: %v", err)
	}
	return d
}

func (e *queueEnv) status(id uuid.UUID) store.DeploymentStatus {
	e.t.Helper()
	d, err := e.st.GetDeployment(e.ctx, id)
	if err != nil || d == nil {
		e.t.Fatalf("get deployment %s: %v", id, err)
	}
	return d.Status
}

// statuses returns project deployment counts by status.
func (e *queueEnv) statuses(p *store.Project) map[store.DeploymentStatus]int {
	e.t.Helper()
	rows, err := e.pool.Query(e.ctx, `SELECT status, COUNT(*) FROM deployments WHERE project_id=$1 GROUP BY status`, p.ID)
	if err != nil {
		e.t.Fatalf("count statuses: %v", err)
	}
	defer rows.Close()
	out := map[store.DeploymentStatus]int{}
	for rows.Next() {
		var s store.DeploymentStatus
		var n int
		_ = rows.Scan(&s, &n)
		out[s] = n
	}
	return out
}

// tasks returns the project's build/deploy tasks as deployment_id → status.
func (e *queueEnv) tasks(p *store.Project) map[uuid.UUID]store.TaskStatus {
	e.t.Helper()
	rows, err := e.pool.Query(e.ctx, `
		SELECT t.deployment_id, t.status FROM tasks t JOIN deployments d ON d.id = t.deployment_id
		WHERE d.project_id=$1 AND t.type IN ('build','deploy')`, p.ID)
	if err != nil {
		e.t.Fatalf("list tasks: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]store.TaskStatus{}
	for rows.Next() {
		var id uuid.UUID
		var s store.TaskStatus
		_ = rows.Scan(&id, &s)
		if _, dup := out[id]; dup {
			e.t.Fatalf("deployment %s has more than one build/deploy task", id)
		}
		out[id] = s
	}
	return out
}

func (e *queueEnv) inFlight(p *store.Project) []uuid.UUID {
	e.t.Helper()
	rows, err := e.pool.Query(e.ctx, `SELECT id FROM deployments WHERE project_id=$1 AND status IN ('pending','building','deploying')`, p.ID)
	if err != nil {
		e.t.Fatalf("list in-flight: %v", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

// finish simulates the agent completing (or failing) the deployment's task the
// way the completeTask handler does, then advances the queue.
func (e *queueEnv) finish(id uuid.UUID, ok bool) {
	e.t.Helper()
	if ok {
		_ = e.st.SetDeploymentHostPort(e.ctx, id, 32000)
		_, _ = e.pool.Exec(e.ctx, `UPDATE tasks SET status='completed' WHERE deployment_id=$1`, id)
	} else {
		_ = e.st.UpdateDeploymentStatus(e.ctx, id, store.DeploymentStatusFailed, "boom")
		_, _ = e.pool.Exec(e.ctx, `UPDATE tasks SET status='failed' WHERE deployment_id=$1`, id)
	}
	e.sched.AdvanceDeployQueueFor(e.ctx, id)
}

func (e *queueEnv) age(table string, id uuid.UUID, by time.Duration) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, fmt.Sprintf(`UPDATE %s SET updated_at = NOW() - make_interval(secs => $2) WHERE id=$1`, table), id, by.Seconds()); err != nil {
		e.t.Fatalf("age %s: %v", table, err)
	}
}

func TestDeployQueue_ConcurrentTriggersRunOneThenOnlyNewest_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.sched.TriggerDeployment(e.ctx, p.ID, "test"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent trigger: %v", err)
	}

	first := e.inFlight(p)
	if len(first) != 1 {
		t.Fatalf("want exactly 1 in-flight deployment after %d concurrent triggers, got %d (%v)", n, len(first), e.statuses(p))
	}
	if tk := e.tasks(p); len(tk) != 1 {
		t.Fatalf("want exactly 1 dispatched task, got %d", len(tk))
	}

	// More triggers while one is in flight all queue up.
	var later []*store.Deployment
	for i := 0; i < 3; i++ {
		d := e.trigger(p)
		if d.Status != store.DeploymentStatusQueued {
			t.Fatalf("trigger while in flight: status %q, want queued", d.Status)
		}
		later = append(later, d)
	}
	newest := later[len(later)-1]

	e.finish(first[0], true)

	second := e.inFlight(p)
	if len(second) != 1 || second[0] != newest.ID {
		t.Fatalf("after the in-flight one finished, want only the newest (%s) in flight, got %v", newest.ID, second)
	}
	c := e.statuses(p)
	if c[store.DeploymentStatusQueued] != 0 {
		t.Fatalf("no deployment should stay queued, got %v", c)
	}
	if c[store.DeploymentStatusSuperseded] != n+3-2 {
		t.Fatalf("want %d superseded, got %v", n+3-2, c)
	}
	if tk := e.tasks(p); len(tk) != 2 {
		t.Fatalf("want exactly 2 dispatched tasks in total, got %d", len(tk))
	}
	d, _ := e.st.GetDeployment(e.ctx, later[0].ID)
	if d.Status != store.DeploymentStatusSuperseded || d.Logs == "" {
		t.Fatalf("older queued deployment: status %q logs %q, want superseded with a reason", d.Status, d.Logs)
	}

	// The newest one failing ends the chain: nothing left to run.
	e.finish(newest.ID, false)
	if got := e.inFlight(p); len(got) != 0 {
		t.Fatalf("nothing should be in flight, got %v", got)
	}
	if tk := e.tasks(p); len(tk) != 2 {
		t.Fatalf("no extra task expected, got %d", len(tk))
	}
}

func TestDeployQueue_FailedInFlightStillAdvances_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	b := e.trigger(p)
	e.finish(a.ID, false)
	if s := e.status(b.ID); s != store.DeploymentStatusPending {
		t.Fatalf("queued deployment after a failure: %q, want pending (dispatched)", s)
	}
}

func TestDeployQueue_LostDeploymentOnDeadNodeIsReaped_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	// The agent died mid-deploy: its node stopped heartbeating long ago and the
	// deployment hasn't moved since.
	e.age("deployments", a.ID, time.Hour)
	if _, err := e.pool.Exec(e.ctx, `UPDATE nodes SET last_seen_at = NOW() - INTERVAL '1 hour' WHERE id=$1`, e.node); err != nil {
		t.Fatal(err)
	}
	// The pinned node is still offline, so dispatching the new deployment fails
	// (surfaced to the caller as before) — but it is marked failed, not left
	// blocking the queue.
	if _, err := e.sched.TriggerDeployment(e.ctx, p.ID, "test"); err == nil {
		t.Fatalf("trigger with the pinned node offline should report the dispatch error")
	}
	if s := e.status(a.ID); s != store.DeploymentStatusFailed {
		t.Fatalf("lost deployment: %q, want failed", s)
	}
	if s := e.tasks(p)[a.ID]; s != store.TaskStatusFailed {
		t.Fatalf("lost deployment's task: %q, want failed so a returning agent won't run it", s)
	}
	if got := e.inFlight(p); len(got) != 0 {
		t.Fatalf("nothing should be in flight, got %v", got)
	}
	// Node comes back: the next trigger runs straight away.
	if _, err := e.pool.Exec(e.ctx, `UPDATE nodes SET last_seen_at = NOW() WHERE id=$1`, e.node); err != nil {
		t.Fatal(err)
	}
	c := e.trigger(p)
	if c.Status != store.DeploymentStatusPending {
		t.Fatalf("trigger after node recovery: %q, want pending", c.Status)
	}
}

func TestDeployQueue_LostDeploymentWithNoTaskIsReaped_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	// Control plane crashed between claiming the deployment and creating its task.
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM tasks WHERE deployment_id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	e.age("deployments", a.ID, 11*time.Minute)
	b := e.trigger(p)
	if s := e.status(a.ID); s != store.DeploymentStatusFailed {
		t.Fatalf("task-less deployment: %q, want failed", s)
	}
	if s := e.status(b.ID); s != store.DeploymentStatusPending {
		t.Fatalf("next deployment: %q, want pending (dispatched)", s)
	}
}

func TestDeployQueue_SilentButAliveDeploymentKeepsBlocking_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	// A 25-minute `docker compose pull` writes no log lines (agents buffer
	// command output), but the agent keeps heartbeating and the task is running.
	_, _ = e.pool.Exec(e.ctx, `UPDATE tasks SET status='running' WHERE deployment_id=$1`, a.ID)
	e.age("deployments", a.ID, 40*time.Minute)
	b := e.trigger(p)
	if s := e.status(a.ID); s != store.DeploymentStatusPending {
		t.Fatalf("silent-but-alive deployment: %q, want still pending (must not be interrupted)", s)
	}
	if s := e.status(b.ID); s != store.DeploymentStatusQueued {
		t.Fatalf("next deployment: %q, want queued", s)
	}

	// Past the hard timeout it is presumed wedged even with a live node.
	e.age("deployments", a.ID, 7*time.Hour)
	e.sched.SweepDeployQueues(e.ctx)
	if s := e.status(a.ID); s != store.DeploymentStatusFailed {
		t.Fatalf("wedged deployment past hard timeout: %q, want failed", s)
	}
	if s := e.status(b.ID); s != store.DeploymentStatusPending {
		t.Fatalf("next deployment after hard timeout: %q, want pending", s)
	}
}

func TestDeployQueue_AgentRestartFailsOrphanAndAdvances_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	_, _ = e.pool.Exec(e.ctx, `UPDATE tasks SET status='running' WHERE deployment_id=$1`, a.ID)
	b := e.trigger(p)
	c := e.trigger(p)

	e.sched.RecoverAgentRestart(e.ctx, e.node)

	if s := e.status(a.ID); s != store.DeploymentStatusFailed {
		t.Fatalf("orphaned deployment: %q, want failed", s)
	}
	if s := e.status(b.ID); s != store.DeploymentStatusSuperseded {
		t.Fatalf("older queued: %q, want superseded", s)
	}
	if s := e.status(c.ID); s != store.DeploymentStatusPending {
		t.Fatalf("newest queued: %q, want pending (dispatched)", s)
	}
	// Its fresh task is pending (not running), so a second register is a no-op.
	e.sched.RecoverAgentRestart(e.ctx, e.node)
	if s := e.status(c.ID); s != store.DeploymentStatusPending {
		t.Fatalf("pending task must survive another register: %q", s)
	}
}

func TestDeployQueue_SweeperDispatchesStrandedQueue_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	b := e.trigger(p)
	// The in-flight one ended without advancing the queue (e.g. the control
	// plane restarted between the status write and the advance).
	_ = e.st.SetDeploymentHostPort(e.ctx, a.ID, 32000)
	if s := e.status(b.ID); s != store.DeploymentStatusQueued {
		t.Fatalf("precondition: %q", s)
	}
	e.sched.SweepDeployQueues(e.ctx)
	if s := e.status(b.ID); s != store.DeploymentStatusPending {
		t.Fatalf("stranded queued deployment after sweep: %q, want pending", s)
	}
}

func TestDeployQueue_PausedWhileQueuedIsNotDispatched_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeImage)
	a := e.trigger(p)
	b := e.trigger(p)
	if _, err := e.pool.Exec(e.ctx, `UPDATE projects SET paused=TRUE WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	e.finish(a.ID, true)
	if s := e.status(b.ID); s != store.DeploymentStatusFailed {
		t.Fatalf("queued deployment of a paused project: %q, want failed", s)
	}
	if _, ok := e.tasks(p)[b.ID]; ok {
		t.Fatalf("paused project's queued deployment must not get a task")
	}
}

func TestDeployQueue_BuildChainHoldsQueueUntilDeployEnds_PG(t *testing.T) {
	e := newQueueEnv(t)
	p := e.project(store.ProjectTypeDeployment)
	a := e.trigger(p)
	if tk := e.tasks(p); tk[a.ID] != store.TaskStatusPending {
		t.Fatalf("deployment-type project should get a build task, got %v", tk)
	}
	// Build done, deploy phase in progress: still one in flight.
	_ = e.st.UpdateDeploymentStatus(e.ctx, a.ID, store.DeploymentStatusDeploying, "")
	b := e.trigger(p)
	if b.Status != store.DeploymentStatusQueued {
		t.Fatalf("trigger during deploy phase: %q, want queued", b.Status)
	}
	e.finish(a.ID, true)
	if s := e.status(b.ID); s != store.DeploymentStatusPending {
		t.Fatalf("after chain ended: %q, want pending", s)
	}
}
