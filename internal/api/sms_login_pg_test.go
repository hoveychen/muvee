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

// fakeVerifyProvider stands in for the Aliyun PNVS provider: it records sends
// and returns a preset CheckCode result. Shared by the SMS pg tests.
type fakeVerifyProvider struct {
	sent []string
	pass bool
}

func (f *fakeVerifyProvider) SendCode(_ context.Context, phone string) error {
	f.sent = append(f.sent, phone)
	return nil
}
func (f *fakeVerifyProvider) CheckCode(_ context.Context, _, _ string) (bool, error) {
	return f.pass, nil
}

// TestSMSLogin_PG is a real-database integration test for the downstream phone
// login endpoints against the PNVS-style provider flow: send records a ledger
// row (rate limiting), the provider owns code verification, and a passing
// verify mints a phone identity via oauth_accounts.
//
//	docker run -d -p 15432:5432 -e POSTGRES_USER=muvee -e POSTGRES_PASSWORD=muvee -e POSTGRES_DB=muvee postgres:16-alpine
//	TEST_DATABASE_URL=postgres://muvee:muvee@localhost:15432/muvee?sslmode=disable JWT_SECRET=test-secret-at-least-32-bytes-long go test ./internal/api/ -run TestSMSLogin_PG
func TestSMSLogin_PG(t *testing.T) {
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
	authSvc, err := auth.New(st)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	fake := &fakeVerifyProvider{}
	s := &Server{store: st, auth: authSvc, smsOverride: fake}

	var ownerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('sms-pg@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, _ := st.GetUserByEmail(ctx, "sms-pg@example.com")
	proj, err := st.CreateProject(ctx, &store.Project{
		Name: "sms-pg-test", ProjectType: store.ProjectTypeDomainOnly,
		DomainPrefix: "sms-pg-test", OwnerID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, proj.ID)
	if _, err := pool.Exec(ctx, `UPDATE projects SET sms_login_enabled = TRUE WHERE id = $1`, proj.ID); err != nil {
		t.Fatalf("enable sms: %v", err)
	}
	pid := proj.ID.String()
	const phone = "+8613800138000"
	defer pool.Exec(ctx, `DELETE FROM sms_verification_codes WHERE phone = $1`, phone)

	key := internalAPIKey()
	post := func(path, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("X-Muvee-Internal-Key", key)
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	sendBody := `{"project_id":"` + pid + `","phone":"` + phone + `"}`

	// 1) Send → 200, provider received the phone, ledger row recorded.
	if w := post("/api/internal/auth/sms/send-code", sendBody, s.handleInternalAuthSMSSendCode); w.Code != http.StatusOK {
		t.Fatalf("send #1: got %d (%s)", w.Code, w.Body.String())
	}
	if len(fake.sent) != 1 || fake.sent[0] != phone {
		t.Fatalf("provider did not receive send for %s: %v", phone, fake.sent)
	}

	// 2) Immediate resend → 429 (60s throttle off the ledger row).
	if w := post("/api/internal/auth/sms/send-code", sendBody, s.handleInternalAuthSMSSendCode); w.Code != http.StatusTooManyRequests {
		t.Fatalf("resend: got %d, want 429", w.Code)
	}

	verifyBody := `{"project_id":"` + pid + `","phone":"` + phone + `","code":"123456"}`

	// 3) Provider rejects → 401.
	fake.pass = false
	if w := post("/api/internal/auth/sms/verify", verifyBody, s.handleInternalAuthSMSVerify); w.Code != http.StatusUnauthorized {
		t.Fatalf("verify (reject): got %d, want 401", w.Code)
	}

	// 4) Provider passes → 200 with a stable phone identity.
	fake.pass = true
	w := post("/api/internal/auth/sms/verify", verifyBody, s.handleInternalAuthSMSVerify)
	if w.Code != http.StatusOK {
		t.Fatalf("verify (pass): got %d (%s)", w.Code, w.Body.String())
	}
	var out struct {
		UserID string `json:"user_id"`
		Phone  string `json:"phone"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.UserID == "" || out.Phone != phone {
		t.Fatalf("verify body user_id=%q phone=%q err=%v", out.UserID, out.Phone, err)
	}
	u, err := authSvc.EnsureIdentityFromOAuth(ctx, "phone", phone, phone, "")
	if err != nil || u.ID.String() != out.UserID {
		t.Fatalf("phone identity not stable: err=%v id=%v want %v", err, u.ID, out.UserID)
	}
}
