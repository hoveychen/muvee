package api

import (
	"encoding/json"
	"net/http"
)

// publicSettingsKeys are safe to expose to unauthenticated callers.
var publicSettingsKeys = []string{"onboarded", "site_name", "logo_url", "favicon_url"}

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
		"onboarded":   true,
		"site_name":   true,
		"logo_url":    true,
		"favicon_url": true,
	}

	ctx := r.Context()
	for k, v := range body {
		if !allowed[k] {
			continue
		}
		if err := s.store.SetSetting(ctx, k, v); err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
	}

	// Return the updated public view
	all, err := s.store.GetAllSettings(ctx)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, all)
}
