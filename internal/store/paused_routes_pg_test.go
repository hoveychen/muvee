package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestGetRunningDeployments_ExcludesPaused_PG is a real-database regression
// test for the production incident where bobo.muveeai.com kept serving a
// *different* project's container after bobo was paused.
//
// Pausing deliberately leaves the deployment row on 'running' (that is how
// resume locates the pinned node again), so the Traefik route query used to
// keep emitting a router for it. Meanwhile the container was stopped, Docker
// put its ephemeral host port back in the pool, and a later deploy of an
// unrelated project grabbed that very port — at which point the paused
// project's domain silently proxied to someone else's app.
//
// Same harness as TestGetProjectByAliasHost_PG: needs TEST_DATABASE_URL.
func TestGetRunningDeployments_ExcludesPaused_PG(t *testing.T) {
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
	if err := Migrate(ctx, pool, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := New(pool)

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email) VALUES ('paused-routes-test@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := s.GetUserByEmail(ctx, "paused-routes-test@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	node, err := s.UpsertNode(ctx, &Node{
		Hostname: "paused-routes-test-node",
		Role:     NodeRoleDeploy,
		HostIP:   "10.99.0.1",
	})
	if err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node.ID)

	proj, err := s.CreateProject(ctx, &Project{
		Name:         "paused-routes-test",
		ProjectType:  ProjectTypeImage,
		DomainPrefix: "paused-routes-test",
		OwnerID:      owner.ID,
		ImageRef:     "example.invalid/app:latest",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)

	dep, err := s.CreateDeployment(ctx, &Deployment{ProjectID: proj.ID, NodeID: &node.ID})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	// SetDeploymentHostPort also flips the row to 'running', exactly as a real
	// successful deploy does.
	if err := s.SetDeploymentHostPort(ctx, dep.ID, 34999); err != nil {
		t.Fatalf("set host port: %v", err)
	}

	contains := func(t *testing.T) bool {
		t.Helper()
		items, err := s.GetRunningDeployments(ctx)
		if err != nil {
			t.Fatalf("GetRunningDeployments: %v", err)
		}
		for _, it := range items {
			if it.DeploymentID == dep.ID {
				return true
			}
		}
		return false
	}

	if !contains(t) {
		t.Fatalf("running deployment %s missing from Traefik route set before pause", dep.ID)
	}

	if err := s.SetProjectPaused(ctx, proj.ID, true); err != nil {
		t.Fatalf("pause project: %v", err)
	}
	if contains(t) {
		t.Fatalf("paused project %s still produces a Traefik route (its container is stopped and the host port may belong to another project now)", proj.ID)
	}

	// Resume must bring the route back without a redeploy: the deployment row
	// was never touched by pause.
	if err := s.SetProjectPaused(ctx, proj.ID, false); err != nil {
		t.Fatalf("resume project: %v", err)
	}
	if !contains(t) {
		t.Fatalf("resumed project %s did not get its Traefik route back", proj.ID)
	}
}
