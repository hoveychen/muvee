package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/store"
)

// TestTraefikConfig_FwdIsolation_PG drives handleTraefikConfig against a real
// database and checks the routing half of BACKLOG-20261117-007:
//
//   - every subsite /_oauth (device-flow) router injects X-Muvee-Aud with the
//     subsite's audience — the project for deployments, aliases and
//     domain_only placeholders and project-bound tunnels, host:<prefix> for a
//     project-less tunnel — including the Cloudflare Tunnel siblings;
//
//   - each ACME base domain's central host gets an /_oauth router that clears
//     X-Muvee-Aud, and so does the Cloudflare Tunnel panel /_oauth router;
//
//   - a base-domain apex registered as an alias produces no router;
//
//   - every embed-bridge instance lists every authservice cookie explicitly;
//
//   - a project-less tunnel's verify URL pins tunnel_host.
//
//     TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestTraefikConfig_FwdIsolation_PG
func TestTraefikConfig_FwdIsolation_PG(t *testing.T) {
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
		`INSERT INTO users (email, name, role) VALUES ('fwdiso-traefik@example.com', 'fi', 'admin')
		 ON CONFLICT (email) DO NOTHING`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, _ := st.GetUserByEmail(ctx, "fwdiso-traefik@example.com")

	// A private deployed project with a custom-domain alias and a (legacy)
	// alias row on a base-domain apex.
	proj, err := st.CreateProject(ctx, &store.Project{Name: "fwdiso", DomainPrefix: "fwdiso", OwnerID: owner.ID})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)
	if _, err := pool.Exec(ctx, `UPDATE projects SET access_mode = 'private', auth_bypass_paths = '/health' WHERE id = $1`, proj.ID); err != nil {
		t.Fatalf("set private: %v", err)
	}
	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (hostname, role, host_ip) VALUES ('fwdiso-node-' || gen_random_uuid(), 'deploy', '10.0.0.10')
		 RETURNING id`).Scan(&nodeID); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO deployments (project_id, status, node_id, host_port) VALUES ($1, 'running', $2, 18081)`,
		proj.ID, nodeID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	if _, err := st.AddProjectAlias(ctx, proj.ID, "fwdiso.example.org"); err != nil {
		t.Fatalf("add alias: %v", err)
	}
	if _, err := st.AddProjectAlias(ctx, proj.ID, "sigma-fi.com"); err != nil {
		t.Fatalf("add apex alias: %v", err)
	}

	// A domain_only project with a live tunnel bound to it, and one without.
	bound, err := st.CreateProject(ctx, &store.Project{
		Name: "fwdiso-bound", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "fwdiso-bound", OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create bound domain_only: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, bound.ID)
	idle, err := st.CreateProject(ctx, &store.Project{
		Name: "fwdiso-idle", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "fwdiso-idle", OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create idle domain_only: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, idle.ID)
	if _, err := pool.Exec(ctx, `UPDATE projects SET access_mode = 'private' WHERE id = ANY($1)`,
		[]string{bound.ID.String(), idle.ID.String()}); err != nil {
		t.Fatalf("set domain_only private: %v", err)
	}

	tunnels := newTunnelRegistry()
	tunnels.register("fwdiso-bound", &tunnelConn{userEmail: "fwdiso-traefik@example.com", connectedAt: time.Now(), projectName: "fwdiso-bound", streams: map[uint32]*tunnelStream{}})
	tunnels.register("t-fwdiso-fox", &tunnelConn{userEmail: "fwdiso-traefik@example.com", authRequired: true, connectedAt: time.Now(), streams: map[uint32]*tunnelStream{}})

	s := &Server{
		store: st, tunnels: tunnels,
		baseDomain:       "opencws.top",
		baseDomains:      []string{"opencws.top", "sigma-fi.com", "cfdemo.com"},
		cfTunnelDomains:  []string{"cfdemo.com"},
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
	assertTraefikRefsResolve(t, &cfg)

	audOf := func(router string) string {
		t.Helper()
		r, ok := cfg.HTTP.Routers[router]
		if !ok {
			t.Errorf("router %q not generated", router)
			return ""
		}
		if len(r.Middlewares) != 1 {
			t.Errorf("router %q middlewares = %v, want exactly one headers middleware", router, r.Middlewares)
			return ""
		}
		mw := cfg.HTTP.Middlewares[r.Middlewares[0]]
		if mw.Headers == nil || mw.ForwardAuth != nil || mw.Plugin != nil {
			t.Errorf("router %q middleware %q = %+v, want a plain headers middleware", router, r.Middlewares[0], mw)
			return ""
		}
		v, ok := mw.Headers.CustomRequestHeaders["X-Muvee-Aud"]
		if !ok || len(mw.Headers.CustomRequestHeaders) != 1 {
			t.Errorf("router %q headers = %v, want only X-Muvee-Aud", router, mw.Headers.CustomRequestHeaders)
		}
		return v
	}

	// Alias routers keep their index in the project's alias list, apex rows
	// included, so locate the custom-domain alias by its rule.
	aliasRouter := ""
	for name, r := range cfg.HTTP.Routers {
		if r.Rule == "Host(`fwdiso.example.org`)" {
			aliasRouter = name
		}
	}
	if !strings.HasPrefix(aliasRouter, "fwdiso-alias-") {
		t.Fatalf("custom-domain alias router not generated (got %q)", aliasRouter)
	}

	projAud := "project:" + proj.ID.String()
	for router, want := range map[string]string{
		"fwdiso-device-flow":                projAud,
		"fwdiso-device-flow_cf":             projAud, // Cloudflare Tunnel sibling
		aliasRouter + "-device-flow":        projAud,
		"tunnel-fwdiso-bound-device-flow":   "project:" + bound.ID.String(),
		"tunnel-t-fwdiso-fox-device-flow":   "host:t-fwdiso-fox",
		"domain-fwdiso-idle-device-flow":    "project:" + idle.ID.String(),
		"domain-fwdiso-idle-device-flow_cf": "project:" + idle.ID.String(),
	} {
		if got := audOf(router); got != want {
			t.Errorf("%s X-Muvee-Aud = %q, want %q", router, got, want)
		}
	}
	// Every subsite /_oauth router carries a non-empty audience.
	for name, r := range cfg.HTTP.Routers {
		if !strings.Contains(name, "-device-flow") {
			continue
		}
		if r.Service != deviceFlowServiceName || r.Priority != 200 {
			t.Errorf("device-flow router %q = %+v", name, r)
		}
		if audOf(name) == "" {
			t.Errorf("device-flow router %q injects an empty audience", name)
		}
	}

	// The apex alias row produces no router of any kind.
	for name, r := range cfg.HTTP.Routers {
		if strings.Contains(r.Rule, "Host(`sigma-fi.com`)") && !strings.HasPrefix(name, "muvee-central-oauth-") {
			t.Errorf("router %q routes the base-domain apex: %q", name, r.Rule)
		}
	}
	aliasRouters := 0
	for name := range cfg.HTTP.Routers {
		if strings.HasPrefix(name, "fwdiso-alias-") && !strings.Contains(name, "-device-flow") && !strings.Contains(name, "-bypass-") {
			aliasRouters++
		}
	}
	if aliasRouters != 1 {
		t.Errorf("found %d alias routers, want 1 (the apex row must be skipped)", aliasRouters)
	}

	// Central /_oauth routers: one per ACME base, X-Muvee-Aud cleared.
	for _, base := range []string{"opencws.top", "sigma-fi.com"} {
		name := centralHostRouterName(base)
		r, ok := cfg.HTTP.Routers[name]
		if !ok {
			t.Errorf("central router %q missing", name)
			continue
		}
		if r.Rule != "Host(`"+base+"`) && PathPrefix(`/_oauth`)" || r.Service != deviceFlowServiceName || r.Priority != 200 {
			t.Errorf("central router %q = %+v", name, r)
		}
		if r.TLS == nil || r.TLS.CertResolver != "letsencrypt" || len(r.TLS.Domains) != 1 || r.TLS.Domains[0].Main != base {
			t.Errorf("central router %q TLS = %+v", name, r.TLS)
		}
		if got := audOf(name); got != "" {
			t.Errorf("central router %q sets X-Muvee-Aud = %q, want it cleared", name, got)
		}
	}
	if _, ok := cfg.HTTP.Routers[centralHostRouterName("cfdemo.com")]; ok {
		t.Error("a Cloudflare Tunnel base got an ACME central router (panel-oauth_cf serves it)")
	}
	if got := audOf("panel-oauth_cf"); got != "" {
		t.Errorf("panel-oauth_cf sets X-Muvee-Aud = %q, want it cleared", got)
	}
	if !strings.Contains(w.Body.String(), `"customRequestHeaders":{"X-Muvee-Aud":""}`) {
		t.Errorf("clear-aud middleware not serialised with an empty value: %s", w.Body.String())
	}

	// Every embed-bridge instance strips every authservice cookie explicitly,
	// and no router still references the host-mounted embed-bridge@file.
	instances := 0
	for name, mw := range cfg.HTTP.Middlewares {
		conf, ok := mw.Plugin["embed-bridge"]
		if !ok {
			continue
		}
		instances++
		raw, _ := json.Marshal(conf["stripCookies"])
		want, _ := json.Marshal(FwdAuthCookieNames)
		if string(raw) != string(want) {
			t.Errorf("embed-bridge instance %q stripCookies = %s, want %s", name, raw, want)
		}
	}
	if instances != 2 {
		t.Errorf("found %d embed-bridge instances, want 2 (project + strip-only)", instances)
	}
	for name, r := range cfg.HTTP.Routers {
		for _, m := range r.Middlewares {
			if m == "embed-bridge@file" {
				t.Errorf("router %q still references embed-bridge@file", name)
			}
		}
	}
	for _, name := range []string{"fwdiso", aliasRouter, "tunnel-fwdiso-bound", "tunnel-t-fwdiso-fox", "domain-fwdiso-idle"} {
		r := cfg.HTTP.Routers[name]
		if n := len(r.Middlewares); n == 0 || r.Middlewares[n-1] != embedBridgeMiddlewareName {
			t.Errorf("router %q middlewares = %v, want %s last", name, r.Middlewares, embedBridgeMiddlewareName)
		}
	}

	// ForwardAuth: project-less tunnel pins tunnel_host; the others pin the
	// project; every one passes the renewed session cookie through.
	wantVerify := map[string]string{
		"fwdiso-auth":              "http://authservice:4181/verify?project_id=" + proj.ID.String(),
		"tunnel-t-fwdiso-fox-auth": "http://authservice:4181/verify?tunnel_host=t-fwdiso-fox",
		"tunnel-fwdiso-bound-auth": "http://authservice:4181/verify?project_id=" + bound.ID.String(),
		"domain-fwdiso-idle-auth":  "http://authservice:4181/verify?project_id=" + idle.ID.String(),
	}
	for name, want := range wantVerify {
		fa := cfg.HTTP.Middlewares[name].ForwardAuth
		if fa == nil {
			t.Errorf("forwardAuth middleware %q missing", name)
			continue
		}
		if fa.Address != want {
			t.Errorf("%s address = %q, want %q", name, fa.Address, want)
		}
		if !reflect.DeepEqual(fa.AddAuthCookiesToResponse, fwdSessionResponseCookies) {
			t.Errorf("%s addAuthCookiesToResponse = %v", name, fa.AddAuthCookiesToResponse)
		}
	}
}
