package store

import (
	"context"
	"os"
	"testing"

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
