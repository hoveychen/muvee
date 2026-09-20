package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

// TestCheckNodeFailovers_SkipsPausedProject_PG is a real-database regression
// test for the production incident where a project paused weeks earlier came
// back to life on its own.
//
// Pausing deliberately leaves the deployment row on 'running' (resume needs it
// to find the pinned node), and node failover treats every 'running' row on a
// dead node as "this must be running somewhere" — so it evicted the paused
// deployment and dispatched a replacement deploy, starting the very container
// the admin had stopped. The paused gate lived only in
// scheduler.TriggerDeployment; failover calls DispatchDeploy directly.
//
// Needs TEST_DATABASE_URL, e.g.:
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./cmd/muvee/ -run TestCheckNodeFailovers_SkipsPausedProject_PG
func TestCheckNodeFailovers_SkipsPausedProject_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(pool)

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email) VALUES ('failover-paused-test@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := st.GetUserByEmail(ctx, "failover-paused-test@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	node, err := st.UpsertNode(ctx, &store.Node{
		Hostname: "failover-paused-test-node",
		Role:     store.NodeRoleDeploy,
		HostIP:   "10.99.0.2",
	})
	if err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node.ID)
	// Make the node look long dead so failover considers it.
	if _, err := pool.Exec(ctx,
		`UPDATE nodes SET last_seen_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, node.ID); err != nil {
		t.Fatalf("age node: %v", err)
	}

	proj, err := st.CreateProject(ctx, &store.Project{
		Name:         "failover-paused-test",
		ProjectType:  store.ProjectTypeImage,
		DomainPrefix: "failover-paused-test",
		OwnerID:      owner.ID,
		ImageRef:     "example.invalid/app:latest",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)

	dep, err := st.CreateDeployment(ctx, &store.Deployment{
		ProjectID: proj.ID,
		NodeID:    &node.ID,
		// A non-empty image tag is what makes failover willing to re-dispatch;
		// the production project that got resurrected had one.
		ImageTag: "example.invalid/app:sha-deadbeef",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if err := st.SetDeploymentHostPort(ctx, dep.ID, 34998); err != nil {
		t.Fatalf("set host port: %v", err)
	}
	if err := st.SetProjectPaused(ctx, proj.ID, true); err != nil {
		t.Fatalf("pause project: %v", err)
	}

	checkNodeFailovers(ctx, st, scheduler.New(st), 3*time.Minute)

	deps, err := st.ListDeployments(ctx, proj.ID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("failover created %d extra deployment(s) for a paused project; want the original one untouched", len(deps)-1)
	}
	if deps[0].ID != dep.ID || deps[0].Status != store.DeploymentStatusRunning {
		t.Fatalf("paused project's deployment was evicted: got id=%s status=%s, want id=%s status=running",
			deps[0].ID, deps[0].Status, dep.ID)
	}
}
