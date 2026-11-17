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

func TestFwdSessionRevoked(t *testing.T) {
	at := time.Unix(1_700_000_000, 500_000_000) // .5s into the second
	cases := []struct {
		revokedAt *time.Time
		iat       int64
		want      bool
	}{
		{nil, 0, false},              // never logged out: legacy sessions untouched
		{nil, at.Unix() - 10, false}, // never logged out
		{&at, 0, true},               // legacy session (no iat) after a logout
		{&at, at.Unix() - 1, true},   // issued before the logout
		{&at, at.Unix(), true},       // same second: fail safe
		{&at, at.Unix() + 1, false},  // signed in again after the logout
	}
	for _, c := range cases {
		if got := fwdSessionRevoked(c.revokedAt, c.iat); got != c.want {
			t.Errorf("fwdSessionRevoked(%v, %d) = %v, want %v", c.revokedAt, c.iat, got, c.want)
		}
	}
}

// TestConsoleLogoutRevokesFwdSessions_PG covers BACKLOG-20261117-008 on the
// server side against a real database: console logout stamps
// users.fwd_sessions_revoked_at from the request's own muvee_session, and both
// the per-project access check and the standalone session check report
// sessions issued at or before it as revoked.
//
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestConsoleLogoutRevokesFwdSessions_PG
func TestConsoleLogoutRevokesFwdSessions_PG(t *testing.T) {
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

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, name, role) VALUES ('revoke-pg@example.com', 'rv', 'member')
		 ON CONFLICT (email) DO UPDATE SET fwd_sessions_revoked_at = NULL`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	user, _ := st.GetUserByEmail(ctx, "revoke-pg@example.com")
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	proj, err := st.CreateProject(ctx, &store.Project{
		Name: "revoke-pg-test", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "revoke-pg-test", OwnerID: user.ID,
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
	access := func(byEmail bool, iat *int64) accessResp {
		q := url.Values{"project_id": {proj.ID.String()}}
		if byEmail {
			q.Set("email", user.Email)
		} else {
			q.Set("user_id", user.ID.String())
		}
		if iat != nil {
			q.Set("session_iat", strconv.FormatInt(*iat, 10))
		}
		var out accessResp
		if code := internal(s.handleInternalAccessCheck, "/api/internal/access/check", q, &out); code != 200 {
			t.Fatalf("access check: %d", code)
		}
		return out
	}
	sessionRevoked := func(byEmail bool, iat int64) bool {
		q := url.Values{"session_iat": {strconv.FormatInt(iat, 10)}}
		if byEmail {
			q.Set("email", user.Email)
		} else {
			q.Set("user_id", user.ID.String())
		}
		var out struct {
			Revoked bool `json:"revoked"`
		}
		if code := internal(s.handleInternalSessionRevoked, "/api/internal/auth/session-revoked", q, &out); code != 200 {
			t.Fatalf("session-revoked: %d", code)
		}
		return out.Revoked
	}
	logout := func(cookie string) int {
		r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "muvee_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.handleLogout(w, r)
		return w.Code
	}

	before := time.Now().Add(-time.Hour).Unix()
	legacy := int64(0)

	// No logout on record: nothing is revoked, legacy sessions included.
	if r := access(true, &before); !r.Allowed || r.SessionRevoked {
		t.Fatalf("before any logout: %+v", r)
	}
	if r := access(false, &legacy); r.SessionRevoked {
		t.Fatalf("legacy session before any logout: %+v", r)
	}
	if sessionRevoked(true, legacy) {
		t.Fatalf("session-revoked before any logout")
	}

	// Logout without a session, or with a forged one, revokes nothing.
	if code := logout(""); code != 200 {
		t.Fatalf("anonymous logout: %d", code)
	}
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{UserID: user.ID.String()}).SignedString([]byte("wrong-secret"))
	logout(forged)
	if sessionRevoked(true, legacy) {
		t.Fatalf("logout without a valid muvee_session revoked sessions")
	}

	// Real console logout.
	console, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		UserID: user.ID.String(), Email: user.Email,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte(secret))
	if code := logout(console); code != 200 {
		t.Fatalf("logout: %d", code)
	}
	var revokedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT fwd_sessions_revoked_at FROM users WHERE id = $1`, user.ID).Scan(&revokedAt); err != nil || revokedAt == nil {
		t.Fatalf("fwd_sessions_revoked_at not stamped: %v %v", revokedAt, err)
	}

	after := time.Now().Add(2 * time.Second).Unix()
	for _, byEmail := range []bool{true, false} {
		if r := access(byEmail, &before); !r.SessionRevoked {
			t.Errorf("access check (byEmail=%v): session issued before logout not revoked: %+v", byEmail, r)
		}
		if r := access(byEmail, &legacy); !r.SessionRevoked {
			t.Errorf("access check (byEmail=%v): legacy session not revoked: %+v", byEmail, r)
		}
		if r := access(byEmail, &after); r.SessionRevoked || !r.Allowed {
			t.Errorf("access check (byEmail=%v): session issued after logout rejected: %+v", byEmail, r)
		}
		if r := access(byEmail, nil); r.SessionRevoked {
			t.Errorf("access check without session_iat (CLI token) reported revoked: %+v", r)
		}
		if !sessionRevoked(byEmail, before) || sessionRevoked(byEmail, after) {
			t.Errorf("session-revoked (byEmail=%v) wrong around the logout", byEmail)
		}
	}

	// Unknown user / bad input.
	if sessionRevokedUnknown := func() bool {
		var out struct {
			Revoked bool `json:"revoked"`
		}
		internal(s.handleInternalSessionRevoked, "/api/internal/auth/session-revoked",
			url.Values{"email": {"nobody-revoke@example.com"}, "session_iat": {"0"}}, &out)
		return out.Revoked
	}(); sessionRevokedUnknown {
		t.Errorf("unknown user reported revoked")
	}
	if code := internal(s.handleInternalSessionRevoked, "/api/internal/auth/session-revoked",
		url.Values{"email": {user.Email}}, nil); code != 400 {
		t.Errorf("missing session_iat: %d, want 400", code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/internal/auth/session-revoked?email=x&session_iat=0", nil)
	w := httptest.NewRecorder()
	s.handleInternalSessionRevoked(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("session-revoked without internal key: %d, want 401", w.Code)
	}
}
