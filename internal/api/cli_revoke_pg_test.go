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

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// TestCLITokenRevocation_PG covers BACKLOG-20261117-009 on the server side
// against a real database: users.cli_tokens_revoked_at is stamped by console
// logout and by POST /api/me/cli-tokens/revoke, and both revocation paths
// (access check and standalone session check) compare a CLI token's cli_iat
// with it — never with fwd_sessions_revoked_at, so an account that signed out
// before the upgrade keeps its CLI tokens until its next logout.
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:15433/postgres?sslmode=disable go test ./internal/api/ -run TestCLITokenRevocation_PG
func TestCLITokenRevocation_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	const secret = "test-secret-at-least-32-bytes-long!!"
	t.Setenv("JWT_SECRET", secret)
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
	s := &Server{store: st, auth: authSvc, tunnels: newTunnelRegistry(), baseDomain: "site-a.test"}

	const email = "cli-revoke-pg@site-a.test"
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, name, role) VALUES ($1, 'cr', 'member')
		 ON CONFLICT (email) DO UPDATE SET fwd_sessions_revoked_at = NULL, cli_tokens_revoked_at = NULL`, email); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	user, _ := st.GetUserByEmail(ctx, email)
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	proj, err := st.CreateProject(ctx, &store.Project{
		Name: "cli-revoke-pg-test", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "cli-revoke-pg-test", OwnerID: user.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)

	internal := func(h http.HandlerFunc, path string, q url.Values, out any) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path+"?"+q.Encode(), nil)
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
	type accessResp struct {
		Allowed        bool `json:"allowed"`
		SessionRevoked bool `json:"session_revoked"`
	}
	ts := func(v int64) string { return strconv.FormatInt(v, 10) }
	// cliAccess / cliSession: the two paths, as a CLI token asks them.
	cliAccess := func(byEmail bool, iat int64) accessResp {
		t.Helper()
		q := url.Values{"project_id": {proj.ID.String()}, "cli_iat": {ts(iat)}, "sids": {"cli-sid-x"}}
		if byEmail {
			q.Set("email", user.Email)
		} else {
			q.Set("user_id", user.ID.String())
		}
		var out accessResp
		if code := internal(s.handleInternalAccessCheck, "/api/internal/access/check", q, &out); code != 200 {
			t.Fatalf("access check: %d", code)
		}
		return out
	}
	cliSession := func(byEmail bool, iat int64) bool {
		t.Helper()
		q := url.Values{"cli_iat": {ts(iat)}, "sids": {"cli-sid-x"}}
		if byEmail {
			q.Set("cli_email", user.Email)
		} else {
			q.Set("cli_user_id", user.ID.String())
		}
		var out struct {
			Revoked bool `json:"revoked"`
		}
		if code := internal(s.handleInternalSessionRevoked, "/api/internal/auth/session-revoked", q, &out); code != 200 {
			t.Fatalf("session-revoked: %d", code)
		}
		return out.Revoked
	}
	// browser session (session_iat) on both paths.
	sessAccess := func(iat int64) bool {
		t.Helper()
		var out accessResp
		internal(s.handleInternalAccessCheck, "/api/internal/access/check",
			url.Values{"project_id": {proj.ID.String()}, "email": {user.Email}, "session_iat": {ts(iat)}}, &out)
		return out.SessionRevoked
	}
	sessSession := func(iat int64) bool {
		t.Helper()
		var out struct {
			Revoked bool `json:"revoked"`
		}
		internal(s.handleInternalSessionRevoked, "/api/internal/auth/session-revoked",
			url.Values{"email": {user.Email}, "session_iat": {ts(iat)}}, &out)
		return out.Revoked
	}
	cliCol := func() *time.Time {
		t.Helper()
		var at *time.Time
		if err := pool.QueryRow(ctx, `SELECT cli_tokens_revoked_at FROM users WHERE id = $1`, user.ID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	consoleJWT := func() string {
		tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
			UserID: user.ID.String(), Email: user.Email,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		}).SignedString([]byte(secret))
		return tok
	}
	logout := func() {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		r.AddCookie(&http.Cookie{Name: "muvee_session", Value: consoleJWT()})
		w := httptest.NewRecorder()
		s.handleLogout(w, r)
		if w.Code != 200 {
			t.Fatalf("logout: %d", w.Code)
		}
	}
	revokeButton := func(asUser *store.User) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/me/cli-tokens/revoke", nil)
		if asUser != nil {
			r = r.WithContext(context.WithValue(r.Context(), auth.CtxUserKey, asUser))
		}
		w := httptest.NewRecorder()
		s.handleRevokeCLITokens(w, r)
		return w.Code
	}

	old := time.Now().Add(-time.Hour).Unix()

	// 1. Upgrade is not retroactive: the account signed out of the console
	// before the upgrade (fwd_sessions_revoked_at set), cli_tokens_revoked_at
	// is still NULL — its CLI tokens keep working on both paths, while its
	// browser sessions from before that logout stay revoked.
	if _, err := pool.Exec(ctx, `UPDATE users SET fwd_sessions_revoked_at = NOW() WHERE id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if cliCol() != nil {
		t.Fatalf("cli_tokens_revoked_at must start NULL")
	}
	for _, byEmail := range []bool{true, false} {
		if r := cliAccess(byEmail, old); r.SessionRevoked || !r.Allowed {
			t.Errorf("upgrade: pre-upgrade CLI token rejected by access check (byEmail=%v): %+v", byEmail, r)
		}
		if cliSession(byEmail, old) {
			t.Errorf("upgrade: pre-upgrade CLI token rejected by session check (byEmail=%v)", byEmail)
		}
		if cliSession(byEmail, 0) {
			t.Errorf("upgrade: CLI token with auth_time 0 rejected (byEmail=%v)", byEmail)
		}
	}
	if !sessAccess(old) || !sessSession(old) {
		t.Errorf("browser session from before the pre-upgrade logout must stay revoked")
	}

	// 2. Console logout stamps both columns: CLI tokens issued at or before it
	// are rejected on both paths, by email and by user id.
	logout()
	cliAt := cliCol()
	if cliAt == nil {
		t.Fatalf("logout did not stamp cli_tokens_revoked_at")
	}
	after := time.Now().Add(2 * time.Second).Unix()
	for _, byEmail := range []bool{true, false} {
		if r := cliAccess(byEmail, old); !r.SessionRevoked {
			t.Errorf("logout: old CLI token passed access check (byEmail=%v): %+v", byEmail, r)
		}
		if !cliSession(byEmail, old) {
			t.Errorf("logout: old CLI token passed session check (byEmail=%v)", byEmail)
		}
		if r := cliAccess(byEmail, cliAt.Unix()); !r.SessionRevoked {
			t.Errorf("same-second CLI token must count as revoked (byEmail=%v)", byEmail)
		}
		// Re-running the device flow afterwards yields a working token.
		if r := cliAccess(byEmail, after); r.SessionRevoked || !r.Allowed {
			t.Errorf("CLI token issued after logout rejected (byEmail=%v): %+v", byEmail, r)
		}
		if cliSession(byEmail, after) {
			t.Errorf("CLI token issued after logout rejected by session check (byEmail=%v)", byEmail)
		}
	}

	// 3. The button revokes CLI tokens only: browser sessions signed in after
	// the logout stay valid, fwd_sessions_revoked_at is untouched.
	var fwdBefore, fwdAfter time.Time
	pool.QueryRow(ctx, `SELECT fwd_sessions_revoked_at FROM users WHERE id = $1`, user.ID).Scan(&fwdBefore)
	time.Sleep(1100 * time.Millisecond) // move past the logout's second
	freshSession := time.Now().Unix()
	if code := revokeButton(nil); code != http.StatusUnauthorized {
		t.Errorf("button without a user: %d, want 401", code)
	}
	time.Sleep(1100 * time.Millisecond)
	if code := revokeButton(user); code != 200 {
		t.Fatalf("button: %d", code)
	}
	pool.QueryRow(ctx, `SELECT fwd_sessions_revoked_at FROM users WHERE id = $1`, user.ID).Scan(&fwdAfter)
	if !fwdAfter.Equal(fwdBefore) {
		t.Errorf("button moved fwd_sessions_revoked_at: %v -> %v", fwdBefore, fwdAfter)
	}
	if sessAccess(freshSession) || sessSession(freshSession) {
		t.Errorf("button revoked a browser session")
	}
	if r := cliAccess(true, after); !r.SessionRevoked {
		t.Errorf("button: CLI token issued before it passed: %+v", r)
	}
	// after (step 2's now+2s) is before the button, which ran ~2.2s later.
	if !cliSession(false, after) {
		t.Errorf("button: CLI token issued before it passed the session check")
	}
	if r := cliAccess(true, time.Now().Add(2*time.Second).Unix()); r.SessionRevoked {
		t.Errorf("CLI token issued after the button rejected: %+v", r)
	}

	// 4. Bad input.
	for name, q := range map[string]url.Values{
		"cli_iat without identity": {"cli_iat": {"0"}},
		"cli identity without iat": {"cli_email": {user.Email}},
		"bad cli_iat":              {"cli_email": {user.Email}, "cli_iat": {"x"}},
		"two identities":           {"email": {user.Email}, "session_iat": {"0"}, "cli_email": {user.Email}, "cli_iat": {"0"}},
	} {
		if code := internal(s.handleInternalSessionRevoked, "/api/internal/auth/session-revoked", q, nil); code != 400 {
			t.Errorf("session-revoked %s: %d, want 400", name, code)
		}
	}
	if code := internal(s.handleInternalAccessCheck, "/api/internal/access/check",
		url.Values{"project_id": {proj.ID.String()}, "email": {user.Email}, "cli_iat": {"x"}}, nil); code != 400 {
		t.Errorf("access check bad cli_iat: %d, want 400", code)
	}
}
