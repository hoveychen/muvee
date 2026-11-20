package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// TestSubsiteSignins_PG covers the console's "subsite sign-ins" card
// (BACKLOG-20261121-001 X2) against a real database:
//
//   - POST /api/me/fwd-sessions/revoke stamps fwd_sessions_revoked_at only:
//     the caller's subsite sessions issued before it are rejected, CLI tokens
//     (cli_tokens_revoked_at) are untouched, nobody else is affected;
//
//   - GET /api/me/visited-projects lists the caller's recently visited
//     subsites (project_visits by user_id) newest first, with the host on the
//     base domain the console is being used on.
//
//     TEST_DATABASE_URL=postgres://postgres:postgres@localhost:15433/postgres?sslmode=disable go test ./internal/api/ -run TestSubsiteSignins_PG
func TestSubsiteSignins_PG(t *testing.T) {
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
	// Closed after every t.Cleanup below (cleanups run LIFO).
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(pool)
	authSvc, err := auth.New(st)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	s := &Server{store: st, auth: authSvc, tunnels: newTunnelRegistry(),
		baseDomain: "site-a.test", baseDomains: []string{"site-a.test", "site-b.test"}}

	mkUser := func(email string) *store.User {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO users (email, name, role) VALUES ($1, 'x', 'member')
			 ON CONFLICT (email) DO UPDATE SET fwd_sessions_revoked_at = NULL, cli_tokens_revoked_at = NULL`, email); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		u, _ := st.GetUserByEmail(ctx, email)
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID) })
		return u
	}
	me := mkUser("signins-pg-me@site-a.test")
	other := mkUser("signins-pg-other@site-a.test")

	mkProject := func(prefix string, typ store.ProjectType) *store.Project {
		t.Helper()
		p, err := st.CreateProject(ctx, &store.Project{Name: prefix, ProjectType: typ, DomainPrefix: prefix, OwnerID: other.ID})
		if err != nil {
			t.Fatalf("create project %s: %v", prefix, err)
		}
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.ID) })
		return p
	}
	pA := mkProject("signins-pg-a", store.ProjectTypeDeployment)
	pB := mkProject("signins-pg-b", store.ProjectTypeDomainOnly)
	pC := mkProject("signins-pg-c", store.ProjectTypeDeployment)

	now := time.Now().UTC().Truncate(time.Second)
	if err := st.RecordProjectVisitsBatch(ctx, []store.ProjectVisit{
		{ProjectID: pA.ID, UserID: me.ID, LastSeenAt: now.Add(-2 * time.Hour), VisitCount: 3},
		{ProjectID: pB.ID, UserID: me.ID, LastSeenAt: now.Add(-time.Minute), VisitCount: 1},
		{ProjectID: pC.ID, UserID: other.ID, LastSeenAt: now, VisitCount: 9}, // someone else's
	}); err != nil {
		t.Fatalf("record visits: %v", err)
	}

	asUser := func(r *http.Request, u *store.User) *http.Request {
		if u == nil {
			return r
		}
		return r.WithContext(context.WithValue(r.Context(), auth.CtxUserKey, u))
	}

	// ── Store: by user, newest first, limited ──────────────────────────────
	got, err := st.ListUserVisitedProjects(ctx, me.ID, 10)
	if err != nil {
		t.Fatalf("ListUserVisitedProjects: %v", err)
	}
	if len(got) != 2 || got[0].ProjectID != pB.ID || got[1].ProjectID != pA.ID {
		t.Fatalf("visited = %+v, want [b, a]", got)
	}
	if got[1].DomainPrefix != "signins-pg-a" || got[1].ProjectName != "signins-pg-a" || got[1].VisitCount != 3 ||
		!got[1].LastSeenAt.Equal(now.Add(-2*time.Hour)) || got[0].ProjectType != store.ProjectTypeDomainOnly {
		t.Errorf("visited row = %+v", got[1])
	}
	if one, _ := st.ListUserVisitedProjects(ctx, me.ID, 1); len(one) != 1 || one[0].ProjectID != pB.ID {
		t.Errorf("limit 1 = %+v", one)
	}

	// The (user_id, last_seen_at) index backs the lookup (project_visits' key
	// starts with project_id).
	var idx int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE tablename = 'project_visits' AND indexname = 'idx_project_visits_user_lastseen'`).Scan(&idx)
	if idx != 1 {
		t.Errorf("idx_project_visits_user_lastseen missing")
	}

	// ── GET /api/me/visited-projects ───────────────────────────────────────
	visited := func(u *store.User, host string) (int, []map[string]any) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/me/visited-projects", nil)
		r.Header.Set("X-Forwarded-Host", host)
		w := httptest.NewRecorder()
		s.handleListVisitedProjects(w, asUser(r, u))
		var out []map[string]any
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v (%s)", err, w.Body.String())
			}
		}
		return w.Code, out
	}
	if code, _ := visited(nil, "site-a.test"); code != http.StatusUnauthorized {
		t.Errorf("visited without a user: %d, want 401", code)
	}
	code, rows := visited(me, "site-b.test")
	if code != 200 || len(rows) != 2 {
		t.Fatalf("visited: %d %+v", code, rows)
	}
	if rows[0]["host"] != "signins-pg-b.site-b.test" || rows[0]["account_url"] != "https://signins-pg-b.site-b.test/_oauth/me" ||
		rows[0]["project_id"] != pB.ID.String() || rows[0]["name"] != "signins-pg-b" {
		t.Errorf("visited row 0 = %+v (host must follow the console's base domain)", rows[0])
	}
	if _, rows := visited(me, "site-a.test"); len(rows) == 2 && rows[1]["host"] != "signins-pg-a.site-a.test" {
		t.Errorf("visited row 1 on site-a.test = %+v", rows[1])
	}
	if _, rows := visited(other, "site-a.test"); len(rows) != 1 || rows[0]["project_id"] != pC.ID.String() {
		t.Errorf("other user's list = %+v", rows)
	}

	// ── POST /api/me/fwd-sessions/revoke ───────────────────────────────────
	revoke := func(u *store.User) int {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleRevokeFwdSessions(w, asUser(httptest.NewRequest(http.MethodPost, "/api/me/fwd-sessions/revoke", nil), u))
		return w.Code
	}
	sessRevoked := func(u *store.User, iat int64) bool {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/internal/auth/session-revoked?"+url.Values{
			"email": {u.Email}, "session_iat": {strconv.FormatInt(iat, 10)}}.Encode(), nil)
		r.Header.Set("X-Muvee-Internal-Key", internalAPIKey())
		w := httptest.NewRecorder()
		s.handleInternalSessionRevoked(w, r)
		var out struct {
			Revoked bool `json:"revoked"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out.Revoked
	}
	cols := func(u *store.User) (fwd, cli *time.Time) {
		t.Helper()
		pool.QueryRow(ctx, `SELECT fwd_sessions_revoked_at, cli_tokens_revoked_at FROM users WHERE id = $1`, u.ID).Scan(&fwd, &cli)
		return
	}

	if code := revoke(nil); code != http.StatusUnauthorized {
		t.Errorf("revoke without a user: %d, want 401", code)
	}
	old := time.Now().Add(-time.Hour).Unix()
	if sessRevoked(me, old) {
		t.Fatalf("session revoked before the button")
	}
	if code := revoke(me); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	fwd, cli := cols(me)
	if fwd == nil {
		t.Fatalf("revoke did not stamp fwd_sessions_revoked_at")
	}
	if cli != nil {
		t.Errorf("revoke touched cli_tokens_revoked_at: %v", cli)
	}
	if !sessRevoked(me, old) || !sessRevoked(me, fwd.Unix()) {
		t.Errorf("subsite session issued before the button still valid")
	}
	if sessRevoked(me, time.Now().Add(2*time.Second).Unix()) {
		t.Errorf("subsite session signed in after the button rejected")
	}
	if ofwd, _ := cols(other); ofwd != nil || sessRevoked(other, old) {
		t.Errorf("another user's sessions were revoked")
	}
}
