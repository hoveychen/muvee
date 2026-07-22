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

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// TestPlatformSMSLogin_PG is a real-database integration test for the platform
// (admin-plane) phone login against the PNVS-style provider flow: a passing
// verify runs the synthetic-email identity through EnsurePlatformMember, signs
// muvee_session, and maps access_mode to authorized (open ⇒ authorized,
// request ⇒ pending). Uses fakeVerifyProvider (see sms_login_pg_test.go).
//
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable go test ./internal/api/ -run TestPlatformSMSLogin_PG
func TestPlatformSMSLogin_PG(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!!")
	t.Setenv("PLATFORM_PHONE_LOGIN", "true")

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
	fake := &fakeVerifyProvider{pass: true}
	s := &Server{store: st, auth: authSvc, verifyProvider: fake}

	post := func(path, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	login := func(phone string) *httptest.ResponseRecorder {
		if w := post("/auth/phone/send-code", `{"phone":"`+phone+`"}`, s.handlePlatformSMSSendCode); w.Code != http.StatusOK {
			t.Fatalf("send %s: got %d (%s)", phone, w.Code, w.Body.String())
		}
		return post("/auth/phone/verify", `{"phone":"`+phone+`","code":"123456"}`, s.handlePlatformSMSVerify)
	}
	cleanup := func(phone string) {
		synth := auth.SyntheticPhoneEmail(phone)
		pool.Exec(ctx, `DELETE FROM sms_verification_codes WHERE phone=$1`, phone)
		if u, _ := st.GetUserByEmail(ctx, synth); u != nil {
			pool.Exec(ctx, `DELETE FROM platform_members WHERE user_id=$1`, u.ID)
			pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u.ID)
		}
	}

	// ── open mode: verified phone becomes an authorized member ──
	if err := st.SetSetting(ctx, "access_mode", "open"); err != nil {
		t.Fatalf("set open: %v", err)
	}
	const phone = "+8613800138111"
	synth := auth.SyntheticPhoneEmail(phone)
	cleanup(phone)
	defer cleanup(phone)

	w := login(phone)
	if w.Code != http.StatusOK {
		t.Fatalf("verify: got %d (%s)", w.Code, w.Body.String())
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatalf("verify body: ok=%v err=%v (%s)", out.OK, err, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "muvee_session=") {
		t.Fatalf("no muvee_session cookie: %q", w.Header().Get("Set-Cookie"))
	}
	u, err := st.GetUserByEmail(ctx, synth)
	if err != nil || u == nil {
		t.Fatalf("synthetic user missing: err=%v", err)
	}
	pm, err := st.GetPlatformMember(ctx, u.ID)
	if err != nil || pm == nil {
		t.Fatalf("platform member missing: err=%v", err)
	}
	if !pm.Authorized {
		t.Errorf("open mode: member should be authorized")
	}

	// ── request mode: a new phone becomes a PENDING (unauthorized) member ──
	if err := st.SetSetting(ctx, "access_mode", "request"); err != nil {
		t.Fatalf("set request: %v", err)
	}
	const phone2 = "+8613800138222"
	cleanup(phone2)
	defer cleanup(phone2)

	if w := login(phone2); w.Code != http.StatusOK {
		t.Fatalf("verify phone2: got %d (%s)", w.Code, w.Body.String())
	}
	u2, err := st.GetUserByEmail(ctx, auth.SyntheticPhoneEmail(phone2))
	if err != nil || u2 == nil {
		t.Fatalf("phone2 user missing: err=%v", err)
	}
	pm2, err := st.GetPlatformMember(ctx, u2.ID)
	if err != nil || pm2 == nil {
		t.Fatalf("phone2 platform member missing: err=%v", err)
	}
	if pm2.Authorized {
		t.Errorf("request mode: new member must be unauthorized (pending admin approval)")
	}
}
