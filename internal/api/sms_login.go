package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/sms"
)

// SMS login tuning. Codes are short-lived and rate-limited per phone number
// (across all projects) so the endpoint cannot be used to burn SMS quota.
const (
	smsCodeTTL          = 5 * time.Minute
	smsResendInterval   = 60 * time.Second
	smsDailySendCap     = 10
	smsMaxVerifyAttempt = 5
)

// checkInternalKey guards the /api/internal/* endpoints. Both muvee-server and
// muvee-authservice derive the key from JWT_SECRET (see internalAPIKey).
func checkInternalKey(r *http.Request) bool {
	expected := internalAPIKey()
	got := r.Header.Get("X-Muvee-Internal-Key")
	return expected != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func hashSMSCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// generateSMSCode returns a random 6-digit numeric code.
func generateSMSCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// handleInternalAuthSMSSendCode issues and delivers a one-time login code for
// muvee-authservice's downstream phone form. Rate-limited per phone. Requires
// the project to have sms_login_enabled. Authenticated via X-Muvee-Internal-Key.
func (s *Server) handleInternalAuthSMSSendCode(w http.ResponseWriter, r *http.Request) {
	if !checkInternalKey(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		ProjectID string `json:"project_id"`
		Phone     string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), http.StatusBadRequest)
		return
	}
	projectID, err := uuid.Parse(body.ProjectID)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid project_id"), http.StatusBadRequest)
		return
	}
	phone, err := sms.NormalizePhone(body.Phone)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid phone number"), http.StatusBadRequest)
		return
	}

	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if proj == nil {
		jsonErr(w, fmt.Errorf("project not found"), http.StatusNotFound)
		return
	}
	if !proj.SMSLoginEnabled {
		jsonErr(w, fmt.Errorf("sms login is not enabled for this project"), http.StatusForbidden)
		return
	}

	now := time.Now()
	// Resend throttle: at most one code per phone per smsResendInterval.
	recent, err := s.store.CountSMSCodesSince(r.Context(), phone, now.Add(-smsResendInterval))
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if recent > 0 {
		writeSMSRateLimited(w, int(smsResendInterval.Seconds()))
		return
	}
	// Daily cap per phone.
	daily, err := s.store.CountSMSCodesSince(r.Context(), phone, now.Add(-24*time.Hour))
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if daily >= smsDailySendCap {
		writeSMSRateLimited(w, int((24 * time.Hour).Seconds()))
		return
	}

	code, err := generateSMSCode()
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if _, err := s.store.CreateSMSCode(r.Context(), &projectID, phone, hashSMSCode(code), now.Add(smsCodeTTL)); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if err := s.smsSender.SendCode(r.Context(), phone, code); err != nil {
		jsonErr(w, fmt.Errorf("failed to send sms: %w", err), http.StatusBadGateway)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// handleInternalAuthSMSVerify checks a submitted code and, on success, upserts
// the identity via oauth_accounts (provider='phone', provider_user_id=<E.164>)
// -- the same identity-only contract as social/password logins. Returns the
// display fields authservice bakes into the forward JWT. 401 for a wrong or
// expired code, 429 once the per-code attempt cap is exhausted.
func (s *Server) handleInternalAuthSMSVerify(w http.ResponseWriter, r *http.Request) {
	if !checkInternalKey(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		ProjectID string `json:"project_id"`
		Phone     string `json:"phone"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), http.StatusBadRequest)
		return
	}
	projectID, err := uuid.Parse(body.ProjectID)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid project_id"), http.StatusBadRequest)
		return
	}
	phone, err := sms.NormalizePhone(body.Phone)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid phone number"), http.StatusBadRequest)
		return
	}
	if body.Code == "" {
		jsonErr(w, fmt.Errorf("code is required"), http.StatusBadRequest)
		return
	}

	rec, err := s.store.LatestUnconsumedSMSCode(r.Context(), &projectID, phone)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if rec == nil || time.Now().After(rec.ExpiresAt) {
		jsonErr(w, fmt.Errorf("invalid or expired code"), http.StatusUnauthorized)
		return
	}
	if rec.Attempts >= smsMaxVerifyAttempt {
		_ = s.store.ConsumeSMSCode(r.Context(), rec.ID)
		writeSMSRateLimited(w, int(smsResendInterval.Seconds()))
		return
	}
	if subtle.ConstantTimeCompare([]byte(hashSMSCode(body.Code)), []byte(rec.CodeHash)) != 1 {
		_ = s.store.IncrementSMSCodeAttempts(r.Context(), rec.ID)
		jsonErr(w, fmt.Errorf("invalid or expired code"), http.StatusUnauthorized)
		return
	}
	if err := s.store.ConsumeSMSCode(r.Context(), rec.ID); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	user, err := s.auth.EnsureIdentityFromOAuth(r.Context(), "phone", phone, phone, "")
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{
		"user_id":    user.ID.String(),
		"phone":      phone,
		"name":       phone,
		"avatar_url": "",
	})
}

// writeSMSRateLimited emits a 429 with a retry_after hint (seconds).
func writeSMSRateLimited(w http.ResponseWriter, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":       "too many requests, please try again later",
		"retry_after": retryAfter,
	})
}
