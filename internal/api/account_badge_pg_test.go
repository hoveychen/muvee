package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// TestAccountBadge_PG covers the per-subsite account badge switch
// (BACKLOG-20261121-001 P3) against a real database:
//
//   - migration 061: rows that existed before it (inserted without the column)
//     read account_badge = false — existing projects stay badge-free;
//
//   - a project created through the API with no account_badge in the body
//     gets true; an explicit false sticks; PUT /api/projects/:id toggles it;
//
//   - the internal access check returns account_badge for the project, for an
//     admin as well as a regular user, read in the query that resolves the
//     user (authservice's /_oauth/account-ui makes no extra call for it).
//
//     TEST_DATABASE_URL=postgres://postgres:postgres@localhost:15433/postgres?sslmode=disable go test ./internal/api/ -run TestAccountBadge_PG
func TestAccountBadge_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
	t.Setenv("GOOGLE_CLIENT_ID", "test-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-client-secret")

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
	authSvc, err := auth.New(st)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	s := &Server{store: st, auth: authSvc}

	mkUser := func(email, role string) *store.User {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO users (email, name, role) VALUES ($1, 'x', $2) ON CONFLICT (email) DO NOTHING`, email, role); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		u, _ := st.GetUserByEmail(ctx, email)
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID) })
		return u
	}
	owner := mkUser("badge-pg-owner@example.com", "member")
	admin := mkUser("badge-pg-admin@example.com", "admin")
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM projects WHERE domain_prefix LIKE 'badge-pg-%'`) })

	// ── Existing rows: inserted the way pre-061 code did, without the column.
	var legacyID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, domain_prefix, owner_id, auth_required) VALUES ('badge-pg-legacy', 'badge-pg-legacy', $1, true) RETURNING id`,
		owner.ID).Scan(&legacyID); err != nil {
		t.Fatalf("insert legacy project: %v", err)
	}
	var legacyBadge bool
	if err := pool.QueryRow(ctx, `SELECT account_badge FROM projects WHERE id = $1`, legacyID).Scan(&legacyBadge); err != nil {
		t.Fatalf("read legacy account_badge: %v", err)
	}
	if legacyBadge {
		t.Fatalf("existing project account_badge = true, want false (existing projects default off)")
	}

	// ── Created through the API ────────────────────────────────────────────
	create := func(name, extra string) *store.Project {
		t.Helper()
		body := `{"name":"` + name + `","domain_prefix":"` + name + `","project_type":"image","image_ref":"example.invalid/app:latest","container_port":8080,"auth_required":true` + extra + `}`
		r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
		r = r.WithContext(context.WithValue(ctx, auth.CtxUserKey, owner))
		w := httptest.NewRecorder()
		s.createProject(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
		var p store.Project
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		return &p
	}
	fresh := create("badge-pg-new", "")
	optOut := create("badge-pg-off", `,"account_badge":false`)
	for _, c := range []struct {
		p    *store.Project
		want bool
	}{{fresh, true}, {optOut, false}} {
		got, err := st.GetProject(ctx, c.p.ID)
		if err != nil || got == nil {
			t.Fatalf("get project: %v", err)
		}
		if got.AccountBadge != c.want {
			t.Fatalf("%s: account_badge = %v, want %v", c.p.Name, got.AccountBadge, c.want)
		}
	}

	// ── Access check carries the switch ────────────────────────────────────
	access := func(projectID string, u *store.User) map[string]any {
		t.Helper()
		q := url.Values{"project_id": {projectID}, "email": {u.Email}}
		r := httptest.NewRequest(http.MethodGet, "/api/internal/access/check?"+q.Encode(), nil)
		r.Header.Set("X-Muvee-Internal-Key", internalAPIKey())
		w := httptest.NewRecorder()
		s.handleInternalAccessCheck(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("access check: %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode access: %v", err)
		}
		return out
	}
	for _, c := range []struct {
		name      string
		projectID string
		u         *store.User
		want      bool
	}{
		{"new project, owner", fresh.ID.String(), owner, true},
		{"new project, admin", fresh.ID.String(), admin, true},
		{"opted out, owner", optOut.ID.String(), owner, false},
		{"opted out, admin", optOut.ID.String(), admin, false},
		{"legacy, owner", legacyID, owner, false},
	} {
		got := access(c.projectID, c.u)
		if got["allowed"] != true {
			t.Fatalf("%s: not allowed: %v", c.name, got)
		}
		if got["account_badge"] != c.want {
			t.Errorf("%s: account_badge = %v, want %v (body %v)", c.name, got["account_badge"], c.want, got)
		}
	}

	// ── Toggle through the update API ──────────────────────────────────────
	update := func(p *store.Project, body string) {
		t.Helper()
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", p.ID.String())
		r := httptest.NewRequest(http.MethodPut, "/api/projects/"+p.ID.String(), strings.NewReader(body))
		r = r.WithContext(context.WithValue(context.WithValue(ctx, auth.CtxUserKey, owner), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		s.updateProject(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("update: %d %s", w.Code, w.Body.String())
		}
	}
	update(fresh, `{"account_badge":false}`)
	if got := access(fresh.ID.String(), owner); got["account_badge"] != false {
		t.Errorf("after switching off: account_badge = %v", got["account_badge"])
	}
	update(optOut, `{"account_badge":true}`)
	if got := access(optOut.ID.String(), owner); got["account_badge"] != true {
		t.Errorf("after switching on: account_badge = %v", got["account_badge"])
	}
	// An unrelated update leaves the switch alone.
	update(optOut, `{"description":"x"}`)
	if got, _ := st.GetProject(ctx, optOut.ID); got == nil || !got.AccountBadge {
		t.Errorf("unrelated update reset account_badge: %+v", got)
	}
}
