package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/sms"
)

// smsProvider resolves the phone-login VerifyProvider from platform settings
// (falling back to ALIYUN_SMS_* env). All four PNVS creds present => a
// PNVSProvider; otherwise the persistent dev LogVerifyProvider. Resolved
// per-request so admin settings changes take effect without a restart.
func (s *Server) smsProvider(ctx context.Context) sms.VerifyProvider {
	if s.smsOverride != nil {
		return s.smsOverride
	}
	id := s.settingOrEnv(ctx, "sms_access_key_id", "ALIYUN_SMS_ACCESS_KEY_ID")
	secret := s.settingOrEnv(ctx, "sms_access_key_secret", "ALIYUN_SMS_ACCESS_KEY_SECRET")
	sign := s.settingOrEnv(ctx, "sms_sign_name", "ALIYUN_SMS_SIGN_NAME")
	tmpl := s.settingOrEnv(ctx, "sms_template_code", "ALIYUN_SMS_TEMPLATE_CODE")
	if id == "" || secret == "" || sign == "" || tmpl == "" {
		return s.devSMS
	}
	param := s.settingOrEnv(ctx, "sms_template_param", "ALIYUN_SMS_TEMPLATE_PARAM")
	p, err := sms.NewPNVSProvider(id, secret, sign, tmpl, param)
	if err != nil {
		log.Printf("[sms] PNVS provider init failed (%v); using dev fallback", err)
		return s.devSMS
	}
	return p
}

// settingOrEnv returns the system_settings value for key, or the env var when
// the setting is empty/unset. Platform config lives in settings (admin-editable
// via /admin/settings); env is the deployment fallback.
func (s *Server) settingOrEnv(ctx context.Context, key, envName string) string {
	if s.store != nil {
		if v, err := s.store.GetSetting(ctx, key); err == nil && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return os.Getenv(envName)
}

// SMS send-rate limits (per phone, across all projects) so the endpoint cannot
// be used to burn SMS quota. Code generation, expiry and verification are owned
// by the provider (Aliyun PNVS in prod, dev fallback otherwise).
const (
	smsResendInterval = 60 * time.Second
	smsDailySendCap   = 10
)

// checkInternalKey guards the /api/internal/* endpoints. Both muvee-server and
// muvee-authservice derive the key from JWT_SECRET (see internalAPIKey).
func checkInternalKey(r *http.Request) bool {
	expected := internalAPIKey()
	got := r.Header.Get("X-Muvee-Internal-Key")
	return expected != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// smsRateLimited reports whether the phone has hit the resend or daily cap, and
// if so writes a 429. Shared by the downstream and platform send endpoints.
func (s *Server) smsRateLimited(w http.ResponseWriter, r *http.Request, phone string) bool {
	now := time.Now()
	recent, err := s.store.CountSMSCodesSince(r.Context(), phone, now.Add(-smsResendInterval))
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return true
	}
	if recent > 0 {
		writeSMSRateLimited(w, int(smsResendInterval.Seconds()))
		return true
	}
	daily, err := s.store.CountSMSCodesSince(r.Context(), phone, now.Add(-24*time.Hour))
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return true
	}
	if daily >= smsDailySendCap {
		writeSMSRateLimited(w, int((24 * time.Hour).Seconds()))
		return true
	}
	return false
}

// handleInternalAuthSMSSendCode asks the provider to deliver a code for
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

	if s.smsRateLimited(w, r, phone) {
		return
	}
	if err := s.smsProvider(r.Context()).SendCode(r.Context(), phone); err != nil {
		jsonErr(w, fmt.Errorf("failed to send sms: %w", err), http.StatusBadGateway)
		return
	}
	if err := s.store.RecordSMSSend(r.Context(), &projectID, phone); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// handleInternalAuthSMSVerify checks a submitted code via the provider and, on
// success, upserts the identity via oauth_accounts (provider='phone',
// provider_user_id=<E.164>) -- the same identity-only contract as
// social/password logins. Returns the display fields authservice bakes into the
// forward JWT. 401 for a wrong or expired code.
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
	if _, err := uuid.Parse(body.ProjectID); err != nil {
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

	ok, err := s.smsProvider(r.Context()).CheckCode(r.Context(), phone, body.Code)
	if err != nil {
		jsonErr(w, fmt.Errorf("verify failed: %w", err), http.StatusBadGateway)
		return
	}
	if !ok {
		jsonErr(w, fmt.Errorf("invalid or expired code"), http.StatusUnauthorized)
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
