package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

// Per-project multi-use invitation links. A link is bound to one project and
// auto-adds each consumer to that project's project_access_users on first
// sign-in. Lifetime is gated by ExpiresAt (optional) and MaxUses (optional)
// or manual revocation. See migration 042_project_invitation_links.sql.

func (s *Server) listProjectInvitationLinks(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	caller := auth.UserFromCtx(r.Context())
	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil || proj == nil {
		jsonErr(w, err, 404)
		return
	}
	if caller.Role != store.UserRoleAdmin && proj.OwnerID != caller.ID {
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can view invitation links"), 403)
		return
	}
	links, err := s.store.ListProjectInvitationLinks(r.Context(), projectID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, links)
}

func (s *Server) createProjectInvitationLink(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	caller := auth.UserFromCtx(r.Context())
	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil || proj == nil {
		jsonErr(w, err, 404)
		return
	}
	if caller.Role != store.UserRoleAdmin && proj.OwnerID != caller.ID {
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can create invitation links"), 403)
		return
	}

	var body struct {
		// ExpiresInDays is optional; 0 / missing means no expiry.
		ExpiresInDays int `json:"expires_in_days"`
		// MaxUses is optional; 0 / missing means unlimited uses.
		MaxUses int `json:"max_uses"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		jsonErr(w, fmt.Errorf("generate token: %w", err), 500)
		return
	}
	token := "mli_" + hex.EncodeToString(raw)
	hash := auth.HashInviteToken(token)

	var expiresAt *time.Time
	if body.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	var maxUses *int
	if body.MaxUses > 0 {
		v := body.MaxUses
		maxUses = &v
	}

	link, err := s.store.CreateProjectInvitationLink(r.Context(), projectID, caller.ID, token, hash, maxUses, expiresAt)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, link)
}

func (s *Server) deleteProjectInvitationLink(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	linkID, ok := parsePathUUID(w, r, "linkId")
	if !ok {
		return
	}
	caller := auth.UserFromCtx(r.Context())
	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil || proj == nil {
		jsonErr(w, err, 404)
		return
	}
	if caller.Role != store.UserRoleAdmin && proj.OwnerID != caller.ID {
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can revoke invitation links"), 403)
		return
	}
	link, err := s.store.GetInvitationLink(r.Context(), linkID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if link == nil || link.ProjectID == nil || *link.ProjectID != projectID {
		jsonErr(w, fmt.Errorf("invitation link not found in this project"), 404)
		return
	}
	if err := s.store.DeleteInvitationLink(r.Context(), linkID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (s *Server) listProjectInvitationLinkUses(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	linkID, ok := parsePathUUID(w, r, "linkId")
	if !ok {
		return
	}
	caller := auth.UserFromCtx(r.Context())
	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil || proj == nil {
		jsonErr(w, err, 404)
		return
	}
	if caller.Role != store.UserRoleAdmin && proj.OwnerID != caller.ID {
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can view invitation link uses"), 403)
		return
	}
	link, err := s.store.GetInvitationLink(r.Context(), linkID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if link == nil || link.ProjectID == nil || *link.ProjectID != projectID {
		jsonErr(w, fmt.Errorf("invitation link not found in this project"), 404)
		return
	}
	uses, err := s.store.ListInvitationLinkUses(r.Context(), linkID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, uses)
}
