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

// TestMeshResolve_PG is a real-database test for the mesh plumbing on the
// server side: allow_project_access defaults to ON for projects created via the
// API, can be switched off through PUT /api/projects/:id, and
// GET /api/agent/mesh/resolve walks caller → target → owner → switch → running
// deployment against real rows.
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestMeshResolve_PG
func TestMeshResolve_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
	// createProject reaches through s.auth for the enabled_providers whitelist;
	// auth.New refuses to start with zero providers.
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

	user := func(email string) *store.User {
		if _, err := pool.Exec(ctx, `INSERT INTO users (email) VALUES ($1) ON CONFLICT (email) DO NOTHING`, email); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		u, err := st.GetUserByEmail(ctx, email)
		if err != nil || u == nil {
			t.Fatalf("get user: %v", err)
		}
		return u
	}
	owner := user("mesh-pg-owner@example.com")
	stranger := user("mesh-pg-stranger@example.com")
	defer pool.Exec(ctx, `DELETE FROM projects WHERE domain_prefix LIKE 'mesh-pg-%'`)

	create := func(u *store.User, name, extra string) *store.Project {
		t.Helper()
		body := `{"name":"` + name + `","domain_prefix":"` + name + `","project_type":"image","image_ref":"example.invalid/app:latest","container_port":8080` + extra + `}`
		r := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
		r = r.WithContext(context.WithValue(ctx, auth.CtxUserKey, u))
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

	caller := create(owner, "mesh-pg-caller", "")
	target := create(owner, "mesh-pg-target", "")
	optedOut := create(owner, "mesh-pg-optout", `,"allow_project_access":false`)
	foreign := create(stranger, "mesh-pg-foreign", "")

	// Default ON when the body omits the field; explicit false sticks.
	for _, c := range []struct {
		p    *store.Project
		want bool
	}{{caller, true}, {target, true}, {optedOut, false}} {
		got, err := st.GetProject(ctx, c.p.ID)
		if err != nil || got == nil {
			t.Fatalf("get project: %v", err)
		}
		if got.AllowProjectAccess != c.want {
			t.Fatalf("%s: allow_project_access=%v, want %v", c.p.Name, got.AllowProjectAccess, c.want)
		}
	}

	node, err := st.UpsertNode(ctx, &store.Node{Hostname: "mesh-pg-node", Role: store.NodeRoleDeploy, HostIP: "10.98.0.7"})
	if err != nil {
		t.Fatalf("upsert node: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, node.ID)
	for _, p := range []*store.Project{target, optedOut, foreign} {
		dep, err := st.CreateDeployment(ctx, &store.Deployment{ProjectID: p.ID, NodeID: &node.ID})
		if err != nil {
			t.Fatalf("create deployment: %v", err)
		}
		if err := st.SetDeploymentHostPort(ctx, dep.ID, 34101); err != nil {
			t.Fatalf("set host port: %v", err)
		}
	}

	resolve := func(callerPrefix, targetPrefix string) meshResolveResponse {
		t.Helper()
		q := url.Values{"caller": {callerPrefix}, "target": {targetPrefix}}
		r := httptest.NewRequest(http.MethodGet, "/api/agent/mesh/resolve?"+q.Encode(), nil)
		w := httptest.NewRecorder()
		s.handleAgentMeshResolve(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("resolve %s→%s: status %d %s", callerPrefix, targetPrefix, w.Code, w.Body.String())
		}
		var out meshResolveResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode resolve: %v", err)
		}
		return out
	}
	expect := func(callerPrefix, targetPrefix, reason string) meshResolveResponse {
		t.Helper()
		out := resolve(callerPrefix, targetPrefix)
		if out.Allowed != (reason == "") || out.Reason != reason {
			t.Fatalf("resolve %s→%s: allowed=%v reason=%q, want reason %q", callerPrefix, targetPrefix, out.Allowed, out.Reason, reason)
		}
		return out
	}

	ok := expect("mesh-pg-caller", "mesh-pg-target", "")
	if ok.Backend != "10.98.0.7:34101" || ok.CallerProjectID != caller.ID.String() ||
		ok.CallerProjectName != "mesh-pg-caller" || ok.TargetProjectID != target.ID.String() {
		t.Fatalf("allowed body: %+v", ok)
	}
	expect("mesh-pg-nope", "mesh-pg-target", meshReasonCallerNotFound)
	expect("mesh-pg-caller", "mesh-pg-nope", meshReasonTargetNotFound)
	expect("mesh-pg-caller", "mesh-pg-foreign", meshReasonDifferentOwner)
	expect("mesh-pg-caller", "mesh-pg-optout", meshReasonAccessDisabled)
	// The caller itself has no running deployment.
	expect("mesh-pg-target", "mesh-pg-caller", meshReasonTargetNotRunning)

	if err := st.SetProjectPaused(ctx, target.ID, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	expect("mesh-pg-caller", "mesh-pg-target", meshReasonTargetNotRunning)
	if err := st.SetProjectPaused(ctx, target.ID, false); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Switching the target off through the update API is honoured at once.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", target.ID.String())
	r := httptest.NewRequest(http.MethodPut, "/api/projects/"+target.ID.String(), strings.NewReader(`{"allow_project_access":false}`))
	r = r.WithContext(context.WithValue(context.WithValue(ctx, auth.CtxUserKey, owner), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.updateProject(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	expect("mesh-pg-caller", "mesh-pg-target", meshReasonAccessDisabled)
}
