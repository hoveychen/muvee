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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

func TestProjectPrefixForHost(t *testing.T) {
	bases := []string{"opencws.top", "sigma-fi.com"}
	cases := []struct {
		host            string
		prefix          string
		apex, underBase bool
	}{
		{"app.opencws.top", "app", false, true},
		{"app.sigma-fi.com", "app", false, true},
		{"APP.Sigma-Fi.com:443", "app", false, true},
		{"sigma-fi.com", "", true, true},
		{"opencws.top", "", true, true},
		{"shop.example.com", "", false, false},
		{"evilsigma-fi.com", "", false, false},
	}
	for _, c := range cases {
		prefix, apex, under := projectPrefixForHost(strings.ToLower(c.host), bases)
		if prefix != c.prefix || apex != c.apex || under != c.underBase {
			t.Errorf("projectPrefixForHost(%q) = (%q,%v,%v), want (%q,%v,%v)",
				c.host, prefix, apex, under, c.prefix, c.apex, c.underBase)
		}
	}
}

func TestWithExistingProviders(t *testing.T) {
	known := map[string]bool{"google": true}
	got := withExistingProviders(known, " Entra ,google,none")
	if !got["google"] || !got["entra"] || got["none"] {
		t.Fatalf("withExistingProviders = %v", got)
	}
	if known["entra"] {
		t.Fatal("withExistingProviders must not mutate its input")
	}
	// A stale stored provider is accepted on update; a brand-new unknown one is not.
	if v, err := normaliseEnabledProviders("entra,google", got); err != nil || v != "entra,google" {
		t.Fatalf("stale entra rejected: %q %v", v, err)
	}
	if _, err := normaliseEnabledProviders("entra,discord", got); err == nil {
		t.Fatal("newly added unknown provider should still be rejected")
	}
}

// TestMultiDomainPortal_PG covers the BASE_DOMAINS fixes end to end against a
// real database:
//
//   - updateProject keeps accepting a provider stored before an admin disabled it
//
//   - /api/public/projects and /api/runtime/config follow the request's base domain
//
//   - /api/internal/project-by-host resolves `<prefix>.<alias-base>`
//
//     docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//     TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestMultiDomainPortal_PG
func TestMultiDomainPortal_PG(t *testing.T) {
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
	defer pool.Close()
	if err := store.Migrate(ctx, pool, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(pool)
	authSvc, err := auth.New(st)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	s := &Server{
		store: st, auth: authSvc, tunnels: newTunnelRegistry(),
		baseDomain:  "opencws.top",
		baseDomains: []string{"opencws.top", "sigma-fi.com"},
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, name, role) VALUES ('mdp-pg@example.com', 'mdp', 'admin')
		 ON CONFLICT (email) DO UPDATE SET role = 'admin'`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, _ := st.GetUserByEmail(ctx, "mdp-pg@example.com")
	proj, err := st.CreateProject(ctx, &store.Project{
		Name: "mdp-pg-test", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "mdp-pg-test", OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)
	// Simulate a provider that was enabled when the project was configured and
	// has since been switched off platform-wide (entra is not registered here).
	if _, err := pool.Exec(ctx,
		`UPDATE projects SET enabled_providers = 'entra,google' WHERE id = $1`, proj.ID); err != nil {
		t.Fatalf("set stale providers: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO deployments (project_id, status) VALUES ($1, 'running')`, proj.ID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	// --- A: updateProject with a stale provider ---------------------------
	put := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/projects/"+proj.ID.String(), strings.NewReader(body))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", proj.ID.String())
		r = r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, rc), auth.CtxUserKey, owner))
		w := httptest.NewRecorder()
		s.updateProject(w, r)
		return w
	}
	if w := put(`{"description":"edited"}`); w.Code != http.StatusOK {
		t.Fatalf("unrelated edit with stale provider: got %d (%s)", w.Code, w.Body.String())
	}
	got, _ := st.GetProject(ctx, proj.ID)
	if got.EnabledProviders != "entra,google" || got.Description != "edited" {
		t.Fatalf("after edit: providers=%q description=%q", got.EnabledProviders, got.Description)
	}
	if w := put(`{"enabled_providers":"entra,google,discord"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("adding an unknown provider: got %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if w := put(`{"enabled_providers":"google"}`); w.Code != http.StatusOK {
		t.Fatalf("dropping the stale provider: got %d (%s)", w.Code, w.Body.String())
	}
	if w := put(`{"enabled_providers":"google,entra"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("re-adding a disabled provider once removed: got %d, want 400", w.Code)
	}

	// --- B1: public projects / runtime config follow the request host ----
	getJSON := func(h http.HandlerFunc, path, host string, out any) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s on %s: %d (%s)", path, host, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	urlFor := func(host string) string {
		var list []struct {
			DomainPrefix string `json:"domain_prefix"`
			URL          string `json:"url"`
		}
		getJSON(s.handlePublicProjects, "/api/public/projects", host, &list)
		for _, p := range list {
			if p.DomainPrefix == "mdp-pg-test" {
				return p.URL
			}
		}
		t.Fatalf("project missing from public list on %s", host)
		return ""
	}
	for host, want := range map[string]string{
		"sigma-fi.com":     "https://mdp-pg-test.sigma-fi.com",
		"www.sigma-fi.com": "https://mdp-pg-test.sigma-fi.com",
		"opencws.top":      "https://mdp-pg-test.opencws.top",
		"10.0.0.1:8080":    "https://mdp-pg-test.opencws.top", // unknown host → canonical
	} {
		if u := urlFor(host); u != want {
			t.Errorf("public url on %s = %q, want %q", host, u, want)
		}
	}
	for host, want := range map[string]string{"sigma-fi.com": "sigma-fi.com", "opencws.top": "opencws.top"} {
		var cfg struct {
			BaseDomain string `json:"base_domain"`
		}
		getJSON(s.handleRuntimeConfig, "/api/runtime/config", host, &cfg)
		if cfg.BaseDomain != want {
			t.Errorf("runtime base_domain on %s = %q, want %q", host, cfg.BaseDomain, want)
		}
	}

	// --- B2: internal project-by-host on every base domain ---------------
	byHost := func(host string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/internal/project-by-host?host="+host, nil)
		r.Header.Set("X-Muvee-Internal-Key", internalAPIKey())
		w := httptest.NewRecorder()
		s.handleInternalProjectByHost(w, r)
		if w.Code == http.StatusOK {
			var out struct {
				ProjectID string `json:"project_id"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			if out.ProjectID != proj.ID.String() {
				t.Errorf("project-by-host %s resolved %q", host, out.ProjectID)
			}
		}
		return w.Code
	}
	for host, want := range map[string]int{
		"mdp-pg-test.opencws.top":  http.StatusOK,
		"mdp-pg-test.sigma-fi.com": http.StatusOK,
		"sigma-fi.com":             http.StatusNotFound,
		"nope.sigma-fi.com":        http.StatusNotFound,
	} {
		if code := byHost(host); code != want {
			t.Errorf("project-by-host %s = %d, want %d", host, code, want)
		}
	}
}
