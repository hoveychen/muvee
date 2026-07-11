package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// Per-project password ("demo") accounts. Provisioned here by the project
// owner or a system admin -- the downstream login page only *consumes* them
// (see cmd/muvee/authservice.go), there is no self-registration endpoint.
//
// Passwords are stored as bcrypt hashes; the plaintext is accepted exactly
// once (create / reset) and never returned. ProjectPasswordAccount's
// PasswordHash field is json:"-" so list responses can return store rows
// directly.

// demoUsernameRe matches a login username: 1-64 chars, lowercase alphanumeric
// plus dot/underscore/hyphen, starting and ending alphanumeric.
var demoUsernameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$`)

func validateDemoUsername(username string) error {
	if username == "" {
		return errors.New("username is required")
	}
	if !demoUsernameRe.MatchString(username) {
		return errors.New("username must be 1-64 lowercase letters, digits, '.', '_' or '-', starting and ending with a letter or digit")
	}
	return nil
}

// validateDemoPassword bounds the plaintext before hashing. The 72-byte upper
// bound is bcrypt's own input limit -- beyond that the tail is silently
// ignored, so we reject instead.
func validateDemoPassword(password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

func normalizeDemoUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// loadProjectForOwner fetches the project and enforces the same access rule
// as the other per-project management endpoints (aliases, invitation links):
// system admin or project owner only. Writes the error response itself.
func (s *Server) loadProjectForOwner(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return nil, false
	}
	caller := auth.UserFromCtx(r.Context())
	proj, err := s.store.GetProject(r.Context(), projectID)
	if err != nil || proj == nil {
		jsonErr(w, err, 404)
		return nil, false
	}
	if caller.Role != store.UserRoleAdmin && proj.OwnerID != caller.ID {
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can manage demo accounts"), 403)
		return nil, false
	}
	return proj, true
}

func (s *Server) listProjectPasswordAccounts(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProjectForOwner(w, r)
	if !ok {
		return
	}
	accounts, err := s.store.ListProjectPasswordAccounts(r.Context(), proj.ID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, accounts)
}

func (s *Server) createProjectPasswordAccount(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProjectForOwner(w, r)
	if !ok {
		return
	}
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), 400)
		return
	}
	username := normalizeDemoUsername(body.Username)
	if err := validateDemoUsername(username); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := validateDemoPassword(body.Password); err != nil {
		jsonErr(w, err, 400)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	account, err := s.store.CreateProjectPasswordAccount(r.Context(), proj.ID, username, string(hash), strings.TrimSpace(body.DisplayName))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			jsonErr(w, fmt.Errorf("username %q already exists in this project", username), 409)
			return
		}
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, account)
}

func (s *Server) updateProjectPasswordAccount(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProjectForOwner(w, r)
	if !ok {
		return
	}
	accountID, ok := parsePathUUID(w, r, "accountId")
	if !ok {
		return
	}
	var body struct {
		Password    *string `json:"password"`
		DisplayName *string `json:"display_name"`
		Disabled    *bool   `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), 400)
		return
	}
	var passwordHash *string
	if body.Password != nil {
		if err := validateDemoPassword(*body.Password); err != nil {
			jsonErr(w, err, 400)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonErr(w, err, 500)
			return
		}
		h := string(hash)
		passwordHash = &h
	}
	if body.DisplayName != nil {
		trimmed := strings.TrimSpace(*body.DisplayName)
		body.DisplayName = &trimmed
	}
	account, err := s.store.UpdateProjectPasswordAccount(r.Context(), proj.ID, accountID, passwordHash, body.DisplayName, body.Disabled)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if account == nil {
		jsonErr(w, fmt.Errorf("account not found in this project"), 404)
		return
	}
	jsonOK(w, account)
}

func (s *Server) deleteProjectPasswordAccount(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProjectForOwner(w, r)
	if !ok {
		return
	}
	accountID, ok := parsePathUUID(w, r, "accountId")
	if !ok {
		return
	}
	if err := s.store.DeleteProjectPasswordAccount(r.Context(), proj.ID, accountID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
