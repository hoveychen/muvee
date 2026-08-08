package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestGetProjectByAliasHost_PG is a real-database regression test for the
// "column reference \"id\" is ambiguous" (SQLSTATE 42702) failure: the query
// JOINs projects with project_aliases, and both tables have an id column, so
// the SELECT list must use the p.-qualified projectColumnsPrefixed.
//
// It only runs when TEST_DATABASE_URL points at a disposable Postgres with
// permission to apply db/migrations, e.g.:
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/store/ -run TestGetProjectByAliasHost_PG
func TestGetProjectByAliasHost_PG(t *testing.T) {
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

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('alias-test@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := s.GetUserByEmail(ctx, "alias-test@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	proj, err := s.CreateProject(ctx, &Project{
		Name:         "alias-pg-test",
		ProjectType:  ProjectTypeDomainOnly,
		DomainPrefix: "alias-pg-test",
		OwnerID:      owner.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)

	const host = "alias-pg-test.example.org"
	if _, err := s.AddProjectAlias(ctx, proj.ID, host); err != nil {
		t.Fatalf("add alias: %v", err)
	}

	got, err := s.GetProjectByAliasHost(ctx, host)
	if err != nil {
		t.Fatalf("GetProjectByAliasHost: %v", err) // buggy query fails here with SQLSTATE 42702
	}
	if got == nil || got.ID != proj.ID {
		t.Fatalf("GetProjectByAliasHost returned %+v, want project %s", got, proj.ID)
	}

	miss, err := s.GetProjectByAliasHost(ctx, "no-such-host.example.org")
	if err != nil {
		t.Fatalf("GetProjectByAliasHost(miss): %v", err)
	}
	if miss != nil {
		t.Fatalf("expected nil for unknown host, got %+v", miss)
	}
}

// TestPurgeOldProjectTraffic_PG is a real-database regression test for the
// disk-filling bug where project_traffic retention only ran inside
// GetProjectTraffic — i.e. only for a project whose traffic page someone
// happened to open, and only for that one project. Projects nobody looked at
// accumulated rows forever (18.4M rows / 5.2GB on prod, which filled the disk
// and took the platform's own Postgres down).
//
// Retention must be project-agnostic: purging sweeps every project.
//
// Same harness as TestGetProjectByAliasHost_PG: needs TEST_DATABASE_URL.
func TestPurgeOldProjectTraffic_PG(t *testing.T) {
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

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('traffic-purge-test@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		 RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := s.GetUserByEmail(ctx, "traffic-purge-test@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	// Two projects: "watched" stands in for one whose traffic page gets opened,
	// "ignored" for one nobody ever looks at. The bug only spared the latter.
	mk := func(prefix string) *Project {
		// Drop leftovers from an earlier run so the test is re-runnable against
		// the same database.
		pool.Exec(ctx, `DELETE FROM project_traffic WHERE project_id IN
		                (SELECT id FROM projects WHERE domain_prefix = $1)`, prefix)
		pool.Exec(ctx, `DELETE FROM projects WHERE domain_prefix = $1`, prefix)
		p, err := s.CreateProject(ctx, &Project{
			Name:         prefix,
			ProjectType:  ProjectTypeDomainOnly,
			DomainPrefix: prefix,
			OwnerID:      owner.ID,
		})
		if err != nil {
			t.Fatalf("create project %s: %v", prefix, err)
		}
		t.Cleanup(func() {
			pool.Exec(ctx, `DELETE FROM project_traffic WHERE project_id = $1`, p.ID)
			pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.ID)
		})
		return p
	}
	watched := mk("purge-watched")
	ignored := mk("purge-ignored")

	insert := func(p *Project, age time.Duration) {
		if err := s.InsertProjectTraffic(ctx, &ProjectTraffic{
			ProjectID:  p.ID,
			ObservedAt: time.Now().Add(-age),
			ClientIP:   "10.0.0.1",
			Host:       p.DomainPrefix + ".example.org",
			Method:     "GET",
			Path:       "/",
			Status:     200,
		}); err != nil {
			t.Fatalf("insert traffic for %s: %v", p.DomainPrefix, err)
		}
	}
	for _, p := range []*Project{watched, ignored} {
		insert(p, 30*24*time.Hour) // stale: must be purged
		insert(p, time.Hour)       // fresh: must survive
	}

	if _, err := s.PurgeOldProjectTraffic(ctx, 7*24*time.Hour); err != nil {
		t.Fatalf("PurgeOldProjectTraffic: %v", err)
	}

	for _, p := range []*Project{watched, ignored} {
		var stale, fresh int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FILTER (WHERE observed_at < NOW() - INTERVAL '7 days'),
			        count(*) FILTER (WHERE observed_at >= NOW() - INTERVAL '7 days')
			 FROM project_traffic WHERE project_id = $1`, p.ID).Scan(&stale, &fresh); err != nil {
			t.Fatalf("count traffic for %s: %v", p.DomainPrefix, err)
		}
		if stale != 0 {
			t.Errorf("project %s: %d rows older than 7 days survived the purge, want 0", p.DomainPrefix, stale)
		}
		if fresh != 1 {
			t.Errorf("project %s: %d fresh rows remain, want 1 (purge must not eat recent traffic)", p.DomainPrefix, fresh)
		}
	}
}
