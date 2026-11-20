package api

import (
	"net/http"
	"time"

	"github.com/hoveychen/muvee/internal/auth"
)

// The console's "subsite sign-ins" card (BACKLOG-20261121-001 X2): the
// subsites the user visited recently, each linking to its /_oauth/me account
// page, and "sign out of all subsites".

// handleRevokeFwdSessions — POST /api/me/fwd-sessions/revoke. Stamps the
// caller's fwd_sessions_revoked_at: every subsite session and central SSO
// session of this user issued so far is rejected by authservice, on every
// device and base domain. The console session and CLI device-flow tokens stay
// (compare handleLogout / handleRevokeCLITokens). Sessions of another account
// (another email) are not this user's and are untouched.
func (s *Server) handleRevokeFwdSessions(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := s.store.RevokeFwdSessions(r.Context(), user.ID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

type visitedProjectResp struct {
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	ProjectType string    `json:"project_type"`
	Host        string    `json:"host"`
	AccountURL  string    `json:"account_url"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	VisitCount  int64     `json:"visit_count"`
}

// handleListVisitedProjects — GET /api/me/visited-projects: the caller's most
// recently visited subsites. Hosts are put on the base domain the console is
// being used on (sessions are per host, and every project is served under
// every base domain).
func (s *Server) handleListVisitedProjects(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rows, err := s.store.ListUserVisitedProjects(r.Context(), user.ID, 50)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	base := s.baseDomainForHost(requestHost(r))
	out := make([]visitedProjectResp, 0, len(rows))
	for _, v := range rows {
		host := v.DomainPrefix + "." + base
		out = append(out, visitedProjectResp{
			ProjectID:   v.ProjectID.String(),
			Name:        v.ProjectName,
			ProjectType: string(v.ProjectType),
			Host:        host,
			AccountURL:  "https://" + host + "/_oauth/me",
			LastSeenAt:  v.LastSeenAt,
			VisitCount:  v.VisitCount,
		})
	}
	jsonOK(w, out)
}
