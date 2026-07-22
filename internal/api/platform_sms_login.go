package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/sms"
)

// platformPhoneLoginEnabled gates the platform (admin-plane) phone login. Off
// unless PLATFORM_PHONE_LOGIN is explicitly enabled. Surfaced to the login page
// via handleListProviders so the form only renders when the endpoints are live.
func platformPhoneLoginEnabled() bool {
	switch os.Getenv("PLATFORM_PHONE_LOGIN") {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}

// handlePlatformSMSSendCode issues and delivers a login code for the platform
// login page. Public (no internal key) but rate-limited per phone. Codes are
// stored with a NULL project_id (platform scope).
func (s *Server) handlePlatformSMSSendCode(w http.ResponseWriter, r *http.Request) {
	if !platformPhoneLoginEnabled() {
		http.Error(w, "phone login is not enabled", http.StatusNotFound)
		return
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), http.StatusBadRequest)
		return
	}
	phone, err := sms.NormalizePhone(body.Phone)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid phone number"), http.StatusBadRequest)
		return
	}

	now := time.Now()
	recent, err := s.store.CountSMSCodesSince(r.Context(), phone, now.Add(-smsResendInterval))
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if recent > 0 {
		writeSMSRateLimited(w, int(smsResendInterval.Seconds()))
		return
	}
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
	// nil project_id => platform-scope code.
	if _, err := s.store.CreateSMSCode(r.Context(), nil, phone, hashSMSCode(code), now.Add(smsCodeTTL)); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	if err := s.smsSender.SendCode(r.Context(), phone, code); err != nil {
		jsonErr(w, fmt.Errorf("failed to send sms: %w", err), http.StatusBadGateway)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// handlePlatformSMSVerify checks a submitted code and, on success, signs a
// muvee_session for the platform admin plane. The phone user flows through the
// existing EnsurePlatformMember policy via a synthetic email (see
// auth.HandlePhoneLogin). Public, but the code is single-use and rate-limited.
func (s *Server) handlePlatformSMSVerify(w http.ResponseWriter, r *http.Request) {
	if !platformPhoneLoginEnabled() {
		http.Error(w, "phone login is not enabled", http.StatusNotFound)
		return
	}
	var body struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), http.StatusBadRequest)
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

	rec, err := s.store.LatestUnconsumedSMSCode(r.Context(), nil, phone)
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
	if hashSMSCode(body.Code) != rec.CodeHash {
		_ = s.store.IncrementSMSCodeAttempts(r.Context(), rec.ID)
		jsonErr(w, fmt.Errorf("invalid or expired code"), http.StatusUnauthorized)
		return
	}
	if err := s.store.ConsumeSMSCode(r.Context(), rec.ID); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	_, jwtToken, err := s.auth.HandlePhoneLogin(r.Context(), phone)
	if err != nil {
		if errors.Is(err, auth.ErrNotInvited) {
			// 200 with an error field so the SPA shows the invite-mode message
			// rather than treating it as a transport failure.
			jsonOK(w, map[string]any{"error": "not_invited"})
			return
		}
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: "muvee_session", Value: jwtToken,
		MaxAge: 7 * 24 * 3600, HttpOnly: true, Path: "/", SameSite: http.SameSiteLaxMode,
	})
	dest := "/"
	if c, err := r.Cookie("muvee_post_login_redirect"); err == nil {
		if safe := safePostLoginRedirect(c.Value); safe != "" {
			dest = safe
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: "muvee_post_login_redirect", Value: "", MaxAge: -1, Path: "/",
	})
	jsonOK(w, map[string]any{"ok": true, "redirect": dest})
}
