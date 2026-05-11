package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hoveychen/muvee/internal/store"
)

// publicSettingsKeys are safe to expose to unauthenticated callers.
// access_mode is exposed so the Login page can show the right hint
// (e.g. "invite-only — contact admin") without requiring a session.
var publicSettingsKeys = []string{"onboarded", "site_name", "logo_url", "favicon_url", "access_mode"}

// handleGetPublicSettings returns branding and onboarding-state settings
// that the frontend needs before the user is authenticated.
func (s *Server) handleGetPublicSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.GetAllSettings(r.Context())
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	out := make(map[string]string, len(publicSettingsKeys))
	for _, k := range publicSettingsKeys {
		out[k] = all[k]
	}
	jsonOK(w, out)
}

// handleGetAdminSettings returns all system settings (admin only).
func (s *Server) handleGetAdminSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.store.GetAllSettings(r.Context())
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, all)
}

// handleUpdateAdminSettings accepts a JSON map of key→value pairs and upserts them.
// Only known setting keys are accepted to prevent arbitrary data injection.
func (s *Server) handleUpdateAdminSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}

	// Allowed keys (extend as needed)
	allowed := map[string]bool{
		"onboarded":                                true,
		"site_name":                                true,
		"logo_url":                                 true,
		"favicon_url":                              true,
		"access_mode":                              true,
		"auto_deploy_master_enabled":               true,
		"auto_deploy_poll_interval_seconds":        true,
		"auto_deploy_image_watch_interval_seconds": true,
		// Social OAuth providers (downstream / ForwardAuth only). All
		// values stored as plain strings; "true"/"false" for the
		// *_enabled toggles. ClientSecret + apple_private_key_p8 are
		// sensitive but stored unencrypted at rest -- same threat model
		// as muvee's existing platform-provider env-var path.
		"discord_enabled":         true,
		"discord_client_id":       true,
		"discord_client_secret":   true,
		"discord_redirect_url":    true,
		"facebook_enabled":        true,
		"facebook_client_id":      true,
		"facebook_client_secret":  true,
		"facebook_redirect_url":   true,
		"twitter_enabled":         true,
		"twitter_client_id":       true,
		"twitter_client_secret":   true,
		"twitter_redirect_url":    true,
		"apple_enabled":           true,
		"apple_client_id":         true, // Apple "Services ID"
		"apple_team_id":           true,
		"apple_key_id":            true,
		"apple_private_key_p8":    true, // raw .p8 PEM contents
		"apple_redirect_url":      true,
	}

	ctx := r.Context()
	socialChanged := false
	for k, v := range body {
		if !allowed[k] {
			continue
		}
		if isSocialOAuthSettingKey(k) {
			socialChanged = true
		}
		if k == "access_mode" {
			switch store.AccessMode(v) {
			case store.AccessModeOpen, store.AccessModeInvite, store.AccessModeRequest:
			default:
				jsonErr(w, fmt.Errorf("invalid access_mode: %q", v), http.StatusBadRequest)
				return
			}
		}
		if strings.HasSuffix(k, "_enabled") && v != "true" && v != "false" {
			jsonErr(w, fmt.Errorf("%s must be 'true' or 'false'", k), http.StatusBadRequest)
			return
		}
		if k == "auto_deploy_poll_interval_seconds" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 10 {
				jsonErr(w, fmt.Errorf("auto_deploy_poll_interval_seconds must be an integer >= 10"), http.StatusBadRequest)
				return
			}
		}
		if k == "auto_deploy_image_watch_interval_seconds" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 60 {
				jsonErr(w, fmt.Errorf("auto_deploy_image_watch_interval_seconds must be an integer >= 60"), http.StatusBadRequest)
				return
			}
		}
		if err := s.store.SetSetting(ctx, k, v); err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
		// Leaving the request flow drops any pending requests since they're
		// no longer actionable in open / invite mode.
		if k == "access_mode" && store.AccessMode(v) != store.AccessModeRequest {
			_ = s.store.ClearPendingAuthorizationRequests(ctx)
		}
	}

	if socialChanged {
		// Async: muvee-server already committed the change; failing to notify
		// authservice means it serves stale configs until restart, not a
		// data-loss situation, so we don't block the admin response on it.
		go s.notifyAuthserviceReload()
	}

	// Return the updated public view
	all, err := s.store.GetAllSettings(ctx)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, all)
}

// isSocialOAuthSettingKey is the predicate for keys that, when changed,
// require muvee-authservice to re-read its provider set via the /_oauth/
// internal/reload endpoint. Listed by prefix so adding new social
// providers to the allowlist does not require touching this function.
func isSocialOAuthSettingKey(k string) bool {
	return strings.HasPrefix(k, "discord_") ||
		strings.HasPrefix(k, "facebook_") ||
		strings.HasPrefix(k, "twitter_") ||
		strings.HasPrefix(k, "apple_")
}

// notifyAuthserviceReload POSTs to muvee-authservice's
// /_oauth/internal/reload endpoint so a fresh fetch of social-OAuth
// configs replaces the cached provider set. Fire-and-forget: failures are
// logged but never surfaced to the admin -- the change persists in the DB
// either way and the next authservice restart will pick it up.
func (s *Server) notifyAuthserviceReload() {
	if s.authServiceURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.authServiceURL, "/")+"/_oauth/internal/reload", nil)
	if err != nil {
		log.Printf("notifyAuthserviceReload: build request: %v", err)
		return
	}
	req.Header.Set("X-Muvee-Internal-Key", internalAPIKey())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("notifyAuthserviceReload: post: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("notifyAuthserviceReload: authservice returned %d", resp.StatusCode)
	}
}
