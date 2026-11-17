package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/store"
)

// assertTraefikRefsResolve is a structural invariant for any generated dynamic
// config: every router's service and every middleware it references must be
// declared in the same document. References qualified with "@<provider>"
// (e.g. embed-bridge@file, api@internal) live in another provider and are
// skipped. Traefik drops a router whose service "does not exist", which is how
// BUG-20261117-001 silently broke alias bypass paths.
func assertTraefikRefsResolve(t *testing.T, cfg *traefikDynamicConfig) {
	t.Helper()
	if cfg == nil || cfg.HTTP == nil {
		return
	}
	for name, r := range cfg.HTTP.Routers {
		if !strings.Contains(r.Service, "@") {
			if _, ok := cfg.HTTP.Services[r.Service]; !ok {
				t.Errorf("router %q references service %q which is not declared", name, r.Service)
			}
		}
		for _, mw := range r.Middlewares {
			if strings.Contains(mw, "@") {
				continue
			}
			if _, ok := cfg.HTTP.Middlewares[mw]; !ok {
				t.Errorf("router %q references middleware %q which is not declared", name, mw)
			}
		}
	}
	if cfg.TCP != nil {
		for name, r := range cfg.TCP.Routers {
			if strings.Contains(r.Service, "@") {
				continue
			}
			if _, ok := cfg.TCP.Services[r.Service]; !ok {
				t.Errorf("tcp router %q references service %q which is not declared", name, r.Service)
			}
		}
	}
}

// TestTraefikConfig_AliasBypassService_PG drives handleTraefikConfig against a
// real database with a private project that has an alias and bypass paths
// (BUG-20261117-001: alias bypass routers pointed at a non-existent
// "<name>-alias-N" service).
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestTraefikConfig_AliasBypassService_PG
func TestTraefikConfig_AliasBypassService_PG(t *testing.T) {
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
		`INSERT INTO users (email, name, role) VALUES ('alias-bypass@example.com', 'ab', 'admin')
		 ON CONFLICT (email) DO NOTHING`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, _ := st.GetUserByEmail(ctx, "alias-bypass@example.com")
	proj, err := st.CreateProject(ctx, &store.Project{
		Name: "aliasbp", DomainPrefix: "aliasbp", OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)
	if _, err := pool.Exec(ctx,
		`UPDATE projects SET access_mode = 'private', auth_bypass_paths = E'/health\n/download/*' WHERE id = $1`,
		proj.ID); err != nil {
		t.Fatalf("set bypass: %v", err)
	}
	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (hostname, role, host_ip) VALUES ('alias-bypass-node-' || gen_random_uuid(), 'deploy', '10.0.0.9')
		 RETURNING id`).Scan(&nodeID); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO deployments (project_id, status, node_id, host_port) VALUES ($1, 'running', $2, 18080)`,
		proj.ID, nodeID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	if _, err := st.AddProjectAlias(ctx, proj.ID, "alias-bypass.example.org"); err != nil {
		t.Fatalf("add alias: %v", err)
	}
	// A private domain_only project with bypass paths exercises the tunnel /
	// placeholder branch so the structural invariant covers it too.
	dom, err := st.CreateProject(ctx, &store.Project{
		Name: "aliasbp-dom", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "aliasbp-dom", OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create domain_only project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, dom.ID)
	if _, err := pool.Exec(ctx,
		`UPDATE projects SET access_mode = 'private', auth_bypass_paths = '/health' WHERE id = $1`,
		dom.ID); err != nil {
		t.Fatalf("set domain_only bypass: %v", err)
	}

	s := &Server{
		store: st, tunnels: newTunnelRegistry(),
		baseDomain:       "opencws.top",
		baseDomains:      []string{"opencws.top", "sigma-fi.com"},
		authServiceURL:   "http://authservice:4181",
		tunnelBackendURL: "http://muvee-server:8080",
	}
	w := httptest.NewRecorder()
	s.handleTraefikConfig(w, httptest.NewRequest(http.MethodGet, "/api/traefik/config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("traefik config: %d %s", w.Code, w.Body.String())
	}
	var cfg traefikDynamicConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The alias bypass routers must exist and serve the project's backend.
	found := 0
	for name, r := range cfg.HTTP.Routers {
		if !strings.HasPrefix(name, "aliasbp-alias-") || !strings.Contains(strings.TrimPrefix(name, "aliasbp-alias-"), "-bypass-") {
			continue
		}
		found++
		if r.Service != "aliasbp" {
			t.Errorf("alias bypass router %q service = %q, want project service %q", name, r.Service, "aliasbp")
		}
		if !strings.Contains(r.Rule, "alias-bypass.example.org") {
			t.Errorf("alias bypass router %q rule %q does not match the alias host", name, r.Rule)
		}
	}
	if found != 2 {
		t.Errorf("found %d alias bypass routers, want 2", found)
	}
	if r, ok := cfg.HTTP.Routers["domain-aliasbp-dom-bypass-0"]; !ok || r.Service != "domain-aliasbp-dom" {
		t.Errorf("domain_only bypass router = %+v (present=%v), want service domain-aliasbp-dom", r, ok)
	}
	assertTraefikRefsResolve(t, &cfg)
}
