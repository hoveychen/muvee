package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/store"
)

func TestParseSessionIDs(t *testing.T) {
	cases := map[string][]string{
		"":                               nil,
		"a":                              {"a"},
		" a , ,b ":                       {"a", "b"},
		"a,b,c,d,e,f":                    {"a", "b", "c", "d"},
		strings.Repeat("x", 129) + ",ok": {"ok"},
	}
	for raw, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/?"+url.Values{"sids": {raw}}.Encode(), nil)
		got := parseSessionIDs(r)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("parseSessionIDs(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestIsBaseDomainApex(t *testing.T) {
	s := &Server{baseDomain: "site-a.test", baseDomains: []string{"site-a.test", "site-b.test"}}
	for host, want := range map[string]bool{
		"site-a.test":      true,
		"Site-B.test ":     true,
		"a.site-b.test":    false,
		"site-b.test.evil": false,
		"":                 false,
	} {
		if got := s.isBaseDomainApex(host); got != want {
			t.Errorf("isBaseDomainApex(%q) = %v, want %v", host, got, want)
		}
	}
}

// TestSessionIDRevocation_PG covers the server side of BACKLOG-20261117-007
// per-session revocation against a real database: revoke-sids lists ids,
// the access check and the standalone session check both report a credential
// whose sid or sso_sid is listed as revoked (the latter also without any user,
// for password / SMS sessions), expired rows are ignored, and by-host reports
// the credential audience (projects and, on request, project-less tunnels).
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestSessionIDRevocation_PG
func TestSessionIDRevocation_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
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
	s := &Server{store: st, tunnels: newTunnelRegistry(), baseDomain: "site-a.test", baseDomains: []string{"site-a.test", "site-b.test"}}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, name, role) VALUES ('sid-revoke@example.com', 'sr', 'member')
		 ON CONFLICT (email) DO UPDATE SET fwd_sessions_revoked_at = NULL`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	user, _ := st.GetUserByEmail(ctx, "sid-revoke@example.com")
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	proj, err := st.CreateProject(ctx, &store.Project{
		Name: "sid-revoke", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "sid-revoke", OwnerID: user.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)
	sfx := strconv.FormatInt(time.Now().UnixNano(), 36)
	sid, ssoSID, other := "sid-"+sfx, "sso-"+sfx, "other-"+sfx
	defer pool.Exec(ctx, `DELETE FROM fwd_session_revocations WHERE sid = ANY($1)`, []string{sid, ssoSID, other, "expired-" + sfx})

	call := func(h http.HandlerFunc, method, path string, q url.Values, body any, out any) int {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path+"?"+q.Encode(), rd)
		r.Header.Set("X-Muvee-Internal-Key", internalAPIKey())
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code == http.StatusOK && out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return w.Code
	}
	access := func(sids string) (allowed, revoked bool) {
		var out struct {
			Allowed        bool `json:"allowed"`
			SessionRevoked bool `json:"session_revoked"`
		}
		q := url.Values{"project_id": {proj.ID.String()}, "email": {user.Email}, "session_iat": {strconv.FormatInt(time.Now().Unix(), 10)}}
		if sids != "" {
			q.Set("sids", sids)
		}
		if code := call(s.handleInternalAccessCheck, http.MethodGet, "/api/internal/access/check", q, nil, &out); code != 200 {
			t.Fatalf("access check: %d", code)
		}
		return out.Allowed, out.SessionRevoked
	}
	checked := func(q url.Values) (int, bool) {
		var out struct {
			Revoked bool `json:"revoked"`
		}
		code := call(s.handleInternalSessionRevoked, http.MethodGet, "/api/internal/auth/session-revoked", q, nil, &out)
		return code, out.Revoked
	}
	revoke := func(ids ...string) int {
		return call(s.handleInternalRevokeSessionIDs, http.MethodPost, "/api/internal/auth/revoke-sids", nil, map[string]any{"sids": ids}, nil)
	}

	// Nothing revoked yet.
	if allowed, revoked := access(sid + "," + ssoSID); !allowed || revoked {
		t.Fatalf("before revocation: allowed=%v revoked=%v", allowed, revoked)
	}
	if code, revoked := checked(url.Values{"sids": {sid}}); code != 200 || revoked {
		t.Fatalf("sids-only check before revocation: %d %v", code, revoked)
	}

	// Subsite sign-out revokes the session's sid and its sso_sid.
	if code := revoke(sid, ssoSID); code != 200 {
		t.Fatalf("revoke-sids: %d", code)
	}
	var exp time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM fwd_session_revocations WHERE sid = $1`, ssoSID).Scan(&exp); err != nil {
		t.Fatalf("row not written: %v", err)
	}
	if d := time.Until(exp); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
		t.Errorf("expires_at %v from now, want ~7d", d)
	}
	// Another subsite's session of the same SSO: only its sso_sid matches.
	if _, revoked := access(other + "," + ssoSID); !revoked {
		t.Errorf("access check: session whose sso_sid was revoked not reported revoked")
	}
	if allowed, revoked := access(other); revoked || !allowed {
		t.Errorf("access check: unrelated session reported revoked (allowed=%v)", allowed)
	}
	if _, revoked := access(""); revoked {
		t.Errorf("access check without sids reported revoked")
	}
	// Standalone check: with a user (iat after nothing → only sids matter),
	// and with sids only (password / SMS session, no user row).
	now := strconv.FormatInt(time.Now().Unix(), 10)
	if code, revoked := checked(url.Values{"email": {user.Email}, "session_iat": {now}, "sids": {other + "," + ssoSID}}); code != 200 || !revoked {
		t.Errorf("session-revoked with user + revoked sso_sid: %d %v", code, revoked)
	}
	if code, revoked := checked(url.Values{"user_id": {user.ID.String()}, "session_iat": {now}, "sids": {other}}); code != 200 || revoked {
		t.Errorf("session-revoked with user + unrelated sid: %d %v", code, revoked)
	}
	if code, revoked := checked(url.Values{"sids": {sid}}); code != 200 || !revoked {
		t.Errorf("sids-only session-revoked: %d %v", code, revoked)
	}
	// Re-revoking never shortens a row; expired rows are ignored and purged.
	if _, err := pool.Exec(ctx, `INSERT INTO fwd_session_revocations (sid, expires_at) VALUES ($1, NOW() - interval '1 minute')`, "expired-"+sfx); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	if code, revoked := checked(url.Values{"sids": {"expired-" + sfx}}); code != 200 || revoked {
		t.Errorf("expired revocation still honoured: %d %v", code, revoked)
	}
	if code := revoke(sid); code != 200 {
		t.Fatalf("re-revoke: %d", code)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM fwd_session_revocations WHERE sid = $1`, "expired-"+sfx).Scan(&n)
	if n != 0 {
		t.Errorf("expired row not purged on insert")
	}

	// Bad input.
	if code, _ := checked(url.Values{}); code != 400 {
		t.Errorf("empty session-revoked: %d, want 400", code)
	}
	if code, _ := checked(url.Values{"email": {user.Email}, "sids": {sid}}); code != 400 {
		t.Errorf("user without session_iat: %d, want 400", code)
	}
	if code := revoke(); code != 400 {
		t.Errorf("revoke-sids with no ids: %d, want 400", code)
	}
	if code := revoke("a", "b", "c", "d", "e"); code != 400 {
		t.Errorf("revoke-sids with 5 ids: %d, want 400", code)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/internal/auth/revoke-sids", strings.NewReader(`{"sids":["x"]}`))
	w := httptest.NewRecorder()
	s.handleInternalRevokeSessionIDs(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoke-sids without internal key: %d, want 401", w.Code)
	}

	// by-host reports the audience; a project-less tunnel resolves only when
	// include_tunnel=1 is asked for, and an apex never resolves.
	byHost := func(host string, tunnel bool) (int, map[string]any) {
		q := url.Values{"host": {host}}
		if tunnel {
			q.Set("include_tunnel", "1")
		}
		var out map[string]any
		code := call(s.handleInternalProjectByHost, http.MethodGet, "/api/internal/projects/by-host", q, nil, &out)
		return code, out
	}
	if code, out := byHost("sid-revoke.site-b.test", false); code != 200 || out["aud"] != "project:"+proj.ID.String() {
		t.Errorf("by-host project aud: %d %v", code, out["aud"])
	}
	tunnelPrefix := "t-sidrev-" + sfx
	s.tunnels.register(tunnelPrefix, &tunnelConn{})
	if code, _ := byHost(tunnelPrefix+".site-a.test", false); code != 404 {
		t.Errorf("tunnel without include_tunnel: %d, want 404", code)
	}
	if code, out := byHost(tunnelPrefix+".site-b.test", true); code != 200 || out["aud"] != "host:"+tunnelPrefix || out["project_id"] != nil {
		t.Errorf("tunnel aud: %d %v", code, out)
	}
	if code, _ := byHost("t-not-connected-"+sfx+".site-a.test", true); code != 404 {
		t.Errorf("unknown tunnel: %d, want 404", code)
	}
	if code, _ := byHost("site-b.test", true); code != 404 {
		t.Errorf("apex: %d, want 404", code)
	}
}
