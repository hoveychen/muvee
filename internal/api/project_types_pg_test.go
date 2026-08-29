package api

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// TestCreateProjectTypeWhitelist_PG is a real-database integration test for the
// platform-wide project-type whitelist: with `enabled_project_types` narrowed
// to the non-builder types, POST /api/projects for a `deployment` project is
// rejected with 403 while a `compose` project still goes through. Admins get
// no bypass — the admin in this test is refused too.
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable JWT_SECRET=test-secret-at-least-32-bytes-long go test ./internal/api/ -run TestCreateProjectTypeWhitelist_PG
func TestCreateProjectTypeWhitelist_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
	// auth.New refuses to start with zero providers, and createProject reaches
	// through s.auth for the enabled_providers whitelist. A dummy Google app is
	// enough — no OAuth round-trip happens in this test.
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")

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
	authSvc, err := auth.New(st)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	s := &Server{store: st, auth: authSvc}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email) VALUES ('ptw-pg@example.com') ON CONFLICT (email) DO NOTHING`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := st.GetUserByEmail(ctx, "ptw-pg@example.com")
	if err != nil || owner == nil {
		t.Fatalf("get user: %v", err)
	}
	// Admin role, to prove admins are not exempt from the whitelist.
	owner.Role = store.UserRoleAdmin

	// Restore whatever the DB had so a shared test database isn't left narrowed.
	prev, _ := st.GetSetting(ctx, settingEnabledProjectTypes)
	defer st.SetSetting(ctx, settingEnabledProjectTypes, prev)
	defer pool.Exec(ctx, `DELETE FROM projects WHERE name LIKE 'ptw-pg-%'`)

	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
		r = r.WithContext(context.WithValue(ctx, auth.CtxUserKey, owner))
		w := httptest.NewRecorder()
		s.createProject(w, r)
		return w
	}

	const deploymentBody = `{"name":"ptw-pg-dep","domain_prefix":"ptw-pg-dep","project_type":"deployment","git_url":"https://example.com/x.git","container_port":8080}`
	const composeBody = `{"name":"ptw-pg-com","domain_prefix":"ptw-pg-com","project_type":"compose","git_url":"https://example.com/x.git","expose_service":"web","expose_port":8080}`

	// No restriction configured → both types are creatable.
	if err := st.SetSetting(ctx, settingEnabledProjectTypes, ""); err != nil {
		t.Fatalf("clear setting: %v", err)
	}
	if w := post(deploymentBody); w.Code != 200 {
		t.Fatalf("unrestricted deployment create: got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	pool.Exec(ctx, `DELETE FROM projects WHERE name = 'ptw-pg-dep'`)

	// Narrow to the types that never invoke the builder.
	if err := st.SetSetting(ctx, settingEnabledProjectTypes, "compose,image,domain_only"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	w := post(deploymentBody)
	if w.Code != 403 {
		t.Fatalf("disabled deployment create: got %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "disabled on this platform") {
		t.Errorf("403 body should explain the platform restriction, got %s", w.Body.String())
	}
	if w := post(composeBody); w.Code != 200 {
		t.Fatalf("allowed compose create: got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
}
