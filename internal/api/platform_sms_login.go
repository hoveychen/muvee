package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

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

// handlePlatformSMSSendCode asks the provider to deliver a login code for the
// platform login page. Public (no internal key) but rate-limited per phone.
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

	if s.smsRateLimited(w, r, phone) {
		return
	}
	if err := s.verifyProvider.SendCode(r.Context(), phone); err != nil {
		jsonErr(w, fmt.Errorf("failed to send sms: %w", err), http.StatusBadGateway)
		return
	}
	// nil project_id => platform-scope send-ledger row (for rate limiting).
	if err := s.store.RecordSMSSend(r.Context(), nil, phone); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// handlePlatformSMSVerify checks a submitted code via the provider and, on
// success, signs a muvee_session for the platform admin plane. The phone user
// flows through the existing EnsurePlatformMember policy via a synthetic email
// (see auth.HandlePhoneLogin). Public, rate-limited on send.
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

	ok, err := s.verifyProvider.CheckCode(r.Context(), phone, body.Code)
	if err != nil {
		jsonErr(w, fmt.Errorf("verify failed: %w", err), http.StatusBadGateway)
		return
	}
	if !ok {
		jsonErr(w, fmt.Errorf("invalid or expired code"), http.StatusUnauthorized)
		return
	}

	_, jwtToken, err := s.auth.HandlePhoneLogin(r.Context(), phone)
	if err != nil {
		if errors.Is(err, auth.ErrNotInvited) {
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
