package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/hoveychen/muvee/internal/store"
)

// Reasons returned by the mesh resolve endpoint when allowed=false. The agent's
// mesh gateway maps them to HTTP statuses (*_not_found → 404,
// different_owner / access_disabled → 403, target_not_running → 503).
const (
	meshReasonCallerNotFound   = "caller_not_found"
	meshReasonTargetNotFound   = "target_not_found"
	meshReasonDifferentOwner   = "different_owner"
	meshReasonAccessDisabled   = "access_disabled"
	meshReasonTargetNotRunning = "target_not_running"
)

// meshResolveResponse is the body of GET /api/agent/mesh/resolve. Always sent
// with 200; Allowed carries the decision.
type meshResolveResponse struct {
	Allowed           bool   `json:"allowed"`
	Reason            string `json:"reason"`
	CallerProjectID   string `json:"caller_project_id,omitempty"`
	CallerProjectName string `json:"caller_project_name,omitempty"`
	TargetProjectID   string `json:"target_project_id,omitempty"`
	// Backend is the target's running deployment as <node host_ip>:<host_port>,
	// the same address Traefik routes the public domain to. Only set when
	// Allowed.
	Backend string `json:"backend,omitempty"`
}

// decideMeshResolve applies the mesh access rules in contract order: caller
// exists → target exists → same owner → target allows project access → target
// has a running deployment (paused counts as not running). caller == target is
// allowed. running is the target's GetRunningDeploymentByProject result.
func decideMeshResolve(caller, target *store.Project, running *store.RunningDeploymentInfo) meshResolveResponse {
	var out meshResolveResponse
	if caller == nil {
		out.Reason = meshReasonCallerNotFound
		return out
	}
	out.CallerProjectID = caller.ID.String()
	out.CallerProjectName = caller.Name
	if target == nil {
		out.Reason = meshReasonTargetNotFound
		return out
	}
	out.TargetProjectID = target.ID.String()
	if caller.OwnerID != target.OwnerID {
		out.Reason = meshReasonDifferentOwner
		return out
	}
	if !target.AllowProjectAccess {
		out.Reason = meshReasonAccessDisabled
		return out
	}
	// A paused project keeps its deployment row on 'running' but its container
	// is stopped and the host port may belong to someone else now — same reason
	// GetRunningDeployments drops it from the Traefik routes.
	if target.Paused || running == nil || running.HostIP == "" || running.HostPort <= 0 {
		out.Reason = meshReasonTargetNotRunning
		return out
	}
	out.Allowed = true
	out.Backend = fmt.Sprintf("%s:%d", running.HostIP, running.HostPort)
	return out
}

// handleAgentMeshResolve answers the agent mesh gateway's question "may the
// container of project <caller> reach project <target>, and where is it?".
//
// Route: GET /api/agent/mesh/resolve?caller=<domain_prefix>&target=<domain_prefix>
// Auth: X-Agent-Secret (via agentSecretMiddleware).
func (s *Server) handleAgentMeshResolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	callerPrefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("caller")))
	targetPrefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target")))

	var caller, target *store.Project
	var err error
	if callerPrefix != "" {
		if caller, err = s.store.GetProjectByDomainPrefix(ctx, callerPrefix); err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
	}
	if caller != nil && targetPrefix != "" {
		if target, err = s.store.GetProjectByDomainPrefix(ctx, targetPrefix); err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
	}
	var running *store.RunningDeploymentInfo
	if target != nil && caller.OwnerID == target.OwnerID && target.AllowProjectAccess && !target.Paused {
		if running, err = s.store.GetRunningDeploymentByProject(ctx, target.ID); err != nil {
			jsonErr(w, err, http.StatusInternalServerError)
			return
		}
	}
	jsonOK(w, decideMeshResolve(caller, target, running))
}
