package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hoveychen/muvee/internal/auth"
)

// ─── Email white-list ───────────────────────────────────────────────────────

func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListInvitations(r.Context())
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, out)
}

func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	admin := auth.UserFromCtx(r.Context())
	inv, err := s.store.CreateInvitation(r.Context(), body.Email, admin.ID)
	if err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}
	jsonOK(w, inv)
}

func (s *Server) handleDeleteInvitation(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	if err := s.store.DeleteInvitation(r.Context(), id); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

// ─── Single-use invitation links ────────────────────────────────────────────

func (s *Server) handleListInvitationLinks(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListInvitationLinks(r.Context())
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, out)
}

func (s *Server) handleCreateInvitationLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// ExpiresInDays is optional; 0 / missing means no expiry.
		ExpiresInDays int `json:"expires_in_days"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		jsonErr(w, fmt.Errorf("generate token: %w", err), http.StatusInternalServerError)
		return
	}
	token := "mli_" + hex.EncodeToString(raw)
	hash := auth.HashInviteToken(token)

	var expiresAt *time.Time
	if body.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}

	admin := auth.UserFromCtx(r.Context())
	link, err := s.store.CreateInvitationLink(r.Context(), admin.ID, token, hash, expiresAt)
	if err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	// link.Token is populated by the store call so the admin can copy it once.
	jsonOK(w, link)
}

func (s *Server) handleDeleteInvitationLink(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	if err := s.store.DeleteInvitationLink(r.Context(), id); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
