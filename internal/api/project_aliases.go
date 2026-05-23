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
)

// Per-project custom-domain aliases. A project is always reachable at
// `<domain_prefix>.<base_domain>`; aliases add extra hosts that route to the
// same backend (see internal/api/server.go handleTraefikConfig).
//
// Validation rules enforced here (the DB only enforces lowercase + UNIQUE):
//   - host must be a syntactically valid RFC1123 dotted hostname with at
//     least two labels (a bare label like "foo" is meaningless as a domain).
//   - host must not equal the platform's own base_domain (apex of the
//     platform itself).
//   - host must not end with `.<base_domain>` — that subdomain namespace is
//     owned by the deployment / domain-only routers and would collide with
//     `<domain_prefix>.<base_domain>` patterns.

// hostLabel matches one RFC1123 label: 1–63 chars, alphanumeric, with optional
// internal hyphens; cannot begin or end with a hyphen.
var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validateAliasHost(host, baseDomain string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return errors.New("host is required")
	}
	if len(host) > 253 {
		return errors.New("host is too long")
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return errors.New("host must not start or end with a dot")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return errors.New("host must include at least one dot (e.g. example.com)")
	}
	for _, l := range labels {
		if !hostLabel.MatchString(l) {
			return fmt.Errorf("invalid host label %q", l)
		}
	}
	base := strings.ToLower(strings.TrimSpace(baseDomain))
	if base != "" {
		if host == base {
			return fmt.Errorf("host %q equals the platform base domain", host)
		}
		if strings.HasSuffix(host, "."+base) {
			return fmt.Errorf("host %q is under the platform base domain — use the built-in <prefix>.%s instead", host, base)
		}
	}
	return nil
}

func normalizeAliasHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func (s *Server) listProjectAliases(w http.ResponseWriter, r *http.Request) {
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
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can view aliases"), 403)
		return
	}
	aliases, err := s.store.ListProjectAliasesByProject(r.Context(), projectID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, aliases)
}

func (s *Server) createProjectAlias(w http.ResponseWriter, r *http.Request) {
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
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can add aliases"), 403)
		return
	}
	var body struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, fmt.Errorf("invalid json: %w", err), 400)
		return
	}
	if err := validateAliasHost(body.Host, s.baseDomain); err != nil {
		jsonErr(w, err, 400)
		return
	}
	host := normalizeAliasHost(body.Host)
	alias, err := s.store.AddProjectAlias(r.Context(), projectID, host)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			jsonErr(w, fmt.Errorf("host %q is already claimed by another project", host), 409)
			return
		}
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, alias)
}

func (s *Server) deleteProjectAlias(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	aliasID, ok := parsePathUUID(w, r, "aliasId")
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
		jsonErr(w, fmt.Errorf("only the project owner or a system admin can remove aliases"), 403)
		return
	}
	alias, err := s.store.GetProjectAlias(r.Context(), aliasID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if alias == nil || alias.ProjectID != projectID {
		jsonErr(w, fmt.Errorf("alias not found in this project"), 404)
		return
	}
	if err := s.store.RemoveProjectAlias(r.Context(), aliasID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
