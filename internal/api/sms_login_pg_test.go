package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// capturingSender records the last code handed to it so the verify half of the
// flow can submit the real code. Stands in for the Aliyun/Log sender.
type capturingSender struct{ lastPhone, lastCode string }

func (c *capturingSender) SendCode(_ context.Context, phone, code string) error {
	c.lastPhone, c.lastCode = phone, code
	return nil
}

// TestSMSLogin_PG is a real-database integration test for the phone/SMS login
// endpoints. It drives the actual send-code + verify handlers against a live
// Postgres so the SQL in the store layer (CreateSMSCode / LatestUnconsumedSMSCode
// / CountSMSCodesSince / IncrementSMSCodeAttempts / ConsumeSMSCode) and the
// handler-level expiry, attempt-cap, and resend-throttle logic are exercised
// together — none of which the store-less unit tests can reach.
//
// It only runs when TEST_DATABASE_URL points at a disposable Postgres with
// permission to apply db/migrations, e.g.:
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
	sender := &capturingSender{}
	s := &Server{store: st, auth: authSvc, smsSender: sender}

	// Project with SMS login enabled.
	var ownerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('sms-pg@example.com')
		 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	owner, err := st.GetUserByEmail(ctx, "sms-pg@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
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

	key := internalAPIKey()
	post := func(path, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("X-Muvee-Internal-Key", key)
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	sendBody := `{"project_id":"` + pid + `","phone":"` + phone + `"}`

	// 1) Send a code → 200 and the sender captured it.
	if w := post("/api/internal/auth/sms/send-code", sendBody, s.handleInternalAuthSMSSendCode); w.Code != http.StatusOK {
		t.Fatalf("send #1: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	code := sender.lastCode
	if len(code) != 6 {
		t.Fatalf("captured code %q is not 6 digits", code)
	}

	// 2) Immediate resend → 429 (60s throttle).
	if w := post("/api/internal/auth/sms/send-code", sendBody, s.handleInternalAuthSMSSendCode); w.Code != http.StatusTooManyRequests {
		t.Fatalf("resend: got %d, want 429", w.Code)
	}

	verify := func(c string) *httptest.ResponseRecorder {
		return post("/api/internal/auth/sms/verify",
			`{"project_id":"`+pid+`","phone":"`+phone+`","code":"`+c+`"}`, s.handleInternalAuthSMSVerify)
	}

	// 3) Wrong code → 401 (attempts incremented, code not consumed). The real
	// code is random, so "000000" is wrong except for a 1-in-1e6 collision.
	if code != "000000" {
		if w := verify("000000"); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code: got %d, want 401 (%s)", w.Code, w.Body.String())
		}
	}

	// 4) Correct code → 200 with a phone identity, then it is consumed.
	w := verify(code)
	if w.Code != http.StatusOK {
		t.Fatalf("verify correct: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var out struct {
		UserID string `json:"user_id"`
		Phone  string `json:"phone"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode verify body: %v", err)
	}
	if out.UserID == "" || out.Phone != phone {
		t.Fatalf("verify body user_id=%q phone=%q", out.UserID, out.Phone)
	}
	// The identity must be an oauth_accounts(provider='phone') binding.
	u, err := authSvc.EnsureIdentityFromOAuth(ctx, "phone", phone, phone, "")
	if err != nil || u.ID.String() != out.UserID {
		t.Fatalf("phone identity not stable: err=%v id=%v want %v", err, u.ID, out.UserID)
	}

	// 5) Replay the now-consumed code → 401 (no unconsumed code remains).
	if w := verify(code); w.Code != http.StatusUnauthorized {
		t.Fatalf("replay consumed code: got %d, want 401", w.Code)
	}

	// 6) Expiry: insert an already-expired code directly, verify → 401.
	if _, err := st.CreateSMSCode(ctx, proj.ID, phone, hashSMSCode("111111"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create expired code: %v", err)
	}
	if w := verify("111111"); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired code: got %d, want 401", w.Code)
	}

	// 7) Attempt cap: a fresh code, then exhaust the 5-try cap → 429.
	if _, err := st.CreateSMSCode(ctx, proj.ID, phone, hashSMSCode("222222"), time.Now().Add(smsCodeTTL)); err != nil {
		t.Fatalf("create fresh code: %v", err)
	}
	for i := 0; i < smsMaxVerifyAttempt; i++ {
		if w := verify("999999"); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i, w.Code)
		}
	}
	// Next verify (even with the right code) is refused: attempt cap hit.
	if w := verify("222222"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after cap: got %d, want 429", w.Code)
	}
}
