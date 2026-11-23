package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

// Real-database tests for the node-scoped container status heartbeat
// (mesh-isolation P6, problem (b)): the control plane used to apply a deploy
// agent's host_port report to "the running deployment of this domain_prefix"
// regardless of which node reported it. After a failover the orphan container
// on the recovered node reported its old port and Traefik ended up pointing at
// the replacement node's IP with the orphan's port.
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestContainerStatuses_
type statusFixture struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	st    *store.Store
	srv   *Server
	owner *store.User
	mux   http.Handler
}

func newStatusFixture(t *testing.T) *statusFixture {
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
	if _, err := pool.Exec(ctx, `INSERT INTO users (email) VALUES ('status-node-test@example.com') ON CONFLICT (email) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	owner, err := st.GetUserByEmail(ctx, "status-node-test@example.com")
	if err != nil || owner == nil {
		t.Fatalf("get user: %v", err)
	}
	s := &Server{store: st, sched: scheduler.New(st)}
	r := chi.NewRouter()
	r.Post("/api/agent/tasks/{id}/complete", s.completeTask)
	r.Post("/api/agent/container-statuses", s.handleContainerStatuses)
	return &statusFixture{t: t, ctx: ctx, pool: pool, st: st, srv: s, owner: owner, mux: r}
}

func (f *statusFixture) node(hostname, ip string, online bool) *store.Node {
	f.t.Helper()
	n, err := f.st.UpsertNode(f.ctx, &store.Node{Hostname: hostname, Role: store.NodeRoleDeploy, HostIP: ip})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM nodes WHERE id = $1`, n.ID) })
	if !online {
		if _, err := f.pool.Exec(f.ctx, `UPDATE nodes SET last_seen_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, n.ID); err != nil {
			f.t.Fatal(err)
		}
	}
	return n
}

func (f *statusFixture) project(name string) *store.Project {
	f.t.Helper()
	p, err := f.st.CreateProject(f.ctx, &store.Project{Name: name, DomainPrefix: name, OwnerID: f.owner.ID,
		ProjectType: store.ProjectTypeDeployment, ContainerPort: 8080})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, p.ID) })
	return p
}

// deployment creates a deployment row; port > 0 makes it running on that port.
func (f *statusFixture) deployment(p *store.Project, node *store.Node, port int, status store.DeploymentStatus) *store.Deployment {
	f.t.Helper()
	var nodeID *uuid.UUID
	if node != nil {
		nodeID = &node.ID
	}
	d, err := f.st.CreateDeployment(f.ctx, &store.Deployment{ProjectID: p.ID, NodeID: nodeID, ImageTag: "example.invalid/x:1"})
	if err != nil {
		f.t.Fatal(err)
	}
	if port > 0 {
		if err := f.st.SetDeploymentHostPort(f.ctx, d.ID, port); err != nil {
			f.t.Fatal(err)
		}
	}
	if status != "" {
		if err := f.st.UpdateDeploymentStatus(f.ctx, d.ID, status, ""); err != nil {
			f.t.Fatal(err)
		}
	}
	return d
}

// report posts a status batch the way a deploy agent does; node == nil is an
// agent that predates node-scoped reports.
func (f *statusFixture) report(node *store.Node, body string) {
	f.t.Helper()
	url := "/api/agent/container-statuses"
	if node != nil {
		url += "?node_id=" + node.ID.String()
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, url, strings.NewReader(body)))
	if w.Code != http.StatusOK {
		f.t.Fatalf("report: %d %s", w.Code, w.Body.String())
	}
}

func (f *statusFixture) get(id uuid.UUID) *store.Deployment {
	f.t.Helper()
	d, err := f.st.GetDeployment(f.ctx, id)
	if err != nil || d == nil {
		f.t.Fatalf("get deployment: %v", err)
	}
	return d
}

// After a failover the dead node returns with its orphan container and
// reports it. A node-scoped report must not touch the replacement's row on the
// other node; the replacement node's own reports still apply.
func TestContainerStatuses_OrphanOnOtherNodeDoesNotHijackRoute_PG(t *testing.T) {
	f := newStatusFixture(t)
	a := f.node("status-node-a", "10.96.1.2", true) // came back
	b := f.node("status-node-b", "10.96.1.3", true)
	p := f.project("status-node-hijack")
	old := f.deployment(p, a, 31001, store.DeploymentStatusStopped)
	repl := f.deployment(p, b, 32002, "")

	f.report(a, `[{"domain_prefix":"status-node-hijack","restart_count":7,"oom_killed":true,"host_port":31001}]`)
	got := f.get(repl.ID)
	if got.HostPort != 32002 {
		t.Errorf("orphan report from node A rewrote the replacement's port on node B: %d, want 32002", got.HostPort)
	}
	if got.RestartCount != 0 || got.OOMKilled {
		t.Errorf("orphan report from node A changed the replacement's runtime status: restarts=%d oom=%v", got.RestartCount, got.OOMKilled)
	}
	if o := f.get(old.ID); o.HostPort != 31001 || o.Status != store.DeploymentStatusStopped {
		t.Errorf("stopped original changed: port=%d status=%s", o.HostPort, o.Status)
	}

	f.report(b, `[{"domain_prefix":"status-node-hijack","restart_count":1,"oom_killed":false,"host_port":32003}]`)
	got = f.get(repl.ID)
	if got.HostPort != 32003 || got.RestartCount != 1 {
		t.Errorf("node B's own report: port=%d restarts=%d, want 32003/1", got.HostPort, got.RestartCount)
	}
}

// Agents that predate node-scoped reports send no node_id: they keep exactly
// the old domain_prefix-keyed behaviour (Watchtower upgrades server and agent
// in the same round but in no guaranteed order).
func TestContainerStatuses_LegacyAgentWithoutNodeID_PG(t *testing.T) {
	f := newStatusFixture(t)
	a := f.node("status-legacy-a", "10.96.2.2", true)
	b := f.node("status-legacy-b", "10.96.2.3", true)
	p := f.project("status-legacy")
	repl := f.deployment(p, b, 32002, "")
	_ = a

	f.report(nil, `[{"domain_prefix":"status-legacy","restart_count":4,"oom_killed":true,"host_port":31001}]`)
	got := f.get(repl.ID)
	if got.HostPort != 31001 || got.RestartCount != 4 || !got.OOMKilled {
		t.Errorf("legacy report: port=%d restarts=%d oom=%v, want the old unscoped update 31001/4/true", got.HostPort, got.RestartCount, got.OOMKilled)
	}
	// host_port 0 (older agents yet) still leaves the port alone.
	f.report(nil, `[{"domain_prefix":"status-legacy","restart_count":5,"oom_killed":false}]`)
	if got := f.get(repl.ID); got.HostPort != 31001 || got.RestartCount != 5 {
		t.Errorf("legacy report without host_port: port=%d restarts=%d, want 31001/5", got.HostPort, got.RestartCount)
	}
}

// Single-deploy-node stacks: a node-scoped report updates exactly what the
// legacy report updates — including rows still attached to a node row the
// agent no longer reports as (renamed agent) and rows without a node.
func TestContainerStatuses_SingleNodeSameAsLegacy_PG(t *testing.T) {
	f := newStatusFixture(t)
	only := f.node("status-single-only", "10.96.3.2", true)
	renamedAway := f.node("status-single-oldname", "10.96.3.2", false)

	type row struct {
		name string
		node *store.Node
	}
	rows := []row{{"status-single-a", only}, {"status-single-b", renamedAway}, {"status-single-c", nil}}
	run := func(reporter *store.Node, port, restarts int) []*store.Deployment {
		var out []*store.Deployment
		for _, r := range rows {
			p := f.project(r.name + map[bool]string{true: "-new", false: "-old"}[reporter != nil])
			out = append(out, f.deployment(p, r.node, 30000, ""))
			body, _ := json.Marshal([]map[string]any{{"domain_prefix": p.DomainPrefix, "restart_count": restarts, "oom_killed": true, "host_port": port}})
			f.report(reporter, string(body))
		}
		return out
	}
	legacy := run(nil, 31111, 3)
	scoped := run(only, 31111, 3)
	for i := range rows {
		l, s := f.get(legacy[i].ID), f.get(scoped[i].ID)
		if l.HostPort != s.HostPort || l.RestartCount != s.RestartCount || l.OOMKilled != s.OOMKilled || l.Status != s.Status {
			t.Errorf("%s: legacy agent → port=%d restarts=%d oom=%v status=%s; node-scoped agent → port=%d restarts=%d oom=%v status=%s",
				rows[i].name, l.HostPort, l.RestartCount, l.OOMKilled, l.Status, s.HostPort, s.RestartCount, s.OOMKilled, s.Status)
		}
		if s.HostPort != 31111 {
			t.Errorf("%s: node-scoped report did not apply on a single-node stack (port=%d)", rows[i].name, s.HostPort)
		}
	}
}

// Wiring: the agent's deploy-completed callback retires the previous running
// deployment and queues a cleanup on the node it ran on.
func TestCompleteTask_CrossNodeRetireDispatchesCleanup_PG(t *testing.T) {
	f := newStatusFixture(t)
	a := f.node("status-complete-a", "10.96.4.2", false)
	b := f.node("status-complete-b", "10.96.4.3", true)
	p := f.project("status-complete")
	old := f.deployment(p, a, 31001, "")
	nd := f.deployment(p, nil, 0, store.DeploymentStatusDeploying)
	if err := f.st.SetDeploymentNode(f.ctx, nd.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	tk, err := f.st.CreateTask(f.ctx, &store.Task{Type: store.TaskTypeDeploy, NodeID: &b.ID, DeploymentID: nd.ID, Payload: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agent/tasks/"+tk.ID.String()+"/complete",
		strings.NewReader(`{"status":"completed","host_port":32002}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	if got := f.get(old.ID).Status; got != store.DeploymentStatusStopped {
		t.Errorf("original status=%s, want stopped", got)
	}
	if got := f.get(nd.ID); got.Status != store.DeploymentStatusRunning || got.HostPort != 32002 {
		t.Errorf("new deployment status=%s port=%d, want running/32002", got.Status, got.HostPort)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT COUNT(*) FROM tasks WHERE type='cleanup' AND node_id=$1 AND deployment_id=$2`, a.ID, old.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cleanup tasks on the old node: %d, want 1", n)
	}
}

// Review finding: while a replacement is in flight the original stays running
// on the dead node. A report from the healthy node (where the replacement's
// container already runs) must not rewrite the original's port: the dead
// node's containers may well still be serving behind its own IP.
func TestContainerStatuses_LiveNodeDoesNotRewriteOtherHostRow_PG(t *testing.T) {
	f := newStatusFixture(t)
	dead := f.node("status-other-dead", "10.96.5.2", false)
	healthy := f.node("status-other-healthy", "10.96.5.3", true)
	p := f.project("status-other-host")
	old := f.deployment(p, dead, 31001, "")
	f.report(healthy, `[{"domain_prefix":"status-other-host","restart_count":2,"oom_killed":false,"host_port":32009}]`)
	if got := f.get(old.ID); got.HostPort != 31001 || got.RestartCount != 0 {
		t.Errorf("report from %s rewrote the row on offline host %s: port=%d restarts=%d, want 31001/0",
			healthy.HostIP, dead.HostIP, got.HostPort, got.RestartCount)
	}
}
