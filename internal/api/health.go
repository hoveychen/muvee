package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

// HealthCheckStatus represents the outcome of a single health check.
type HealthCheckStatus string

const (
	HealthCheckOK      HealthCheckStatus = "ok"
	HealthCheckWarning HealthCheckStatus = "warning"
	HealthCheckError   HealthCheckStatus = "error"
)

// HealthCheck is the result of one named diagnostic probe.
type HealthCheck struct {
	Name    string            `json:"name"`
	Status  HealthCheckStatus `json:"status"`
	Message string            `json:"message"`
}

// HealthReport is the full server-side health report returned to admins.
type HealthReport struct {
	Checks    []HealthCheck `json:"checks"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// handleGetSystemHealth runs server-side diagnostics and returns results.
func (s *Server) handleGetSystemHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := []HealthCheck{}

	// ── 1. Internet connectivity ─────────────────────────────────────────────
	checks = append(checks, checkHTTP("internet", "http://example.com", 5*time.Second))

	// ── 2. Traefik proxy ─────────────────────────────────────────────────────
	if s.baseDomain != "" {
		checks = append(checks, checkHTTP("traefik", "http://traefik."+s.baseDomain, 5*time.Second))
	} else {
		checks = append(checks, HealthCheck{
			Name:    "traefik",
			Status:  HealthCheckWarning,
			Message: "BASE_DOMAIN not configured — skipping Traefik check",
		})
	}

	// ── 3. Docker registry ───────────────────────────────────────────────────
	if s.registryAddr != "" {
		scheme := "https"
		addr := s.registryAddr
		if len(addr) > 7 && addr[:7] == "http://" {
			scheme = "http"
		}
		if len(addr) > 8 && addr[:8] == "https://" {
			addr = addr[8:]
		} else if len(addr) > 7 && addr[:7] == "http://" {
			addr = addr[7:]
		}
		registryURL := scheme + "://" + addr + "/v2/"
		check := checkHTTPWithAuth("registry", registryURL, s.registryUser, s.registryPassword, 5*time.Second)
		checks = append(checks, check)
	} else {
		checks = append(checks, HealthCheck{
			Name:    "registry",
			Status:  HealthCheckWarning,
			Message: "REGISTRY_ADDR not configured",
		})
	}

	// ── 4. NFS volume path ───────────────────────────────────────────────────
	checks = append(checks, checkPath("nfs_volume", s.volumeNFSBasePath))

	// ── 5. NFS dataset path ──────────────────────────────────────────────────
	checks = append(checks, checkPath("nfs_dataset", s.datasetNFSBasePath))

	// ── 6. Builder agents ────────────────────────────────────────────────────
	checks = append(checks, s.checkAgents(ctx, "builder"))

	// ── 7. Deploy agents ─────────────────────────────────────────────────────
	checks = append(checks, s.checkAgents(ctx, "deploy"))

	jsonOK(w, HealthReport{
		Checks:    checks,
		UpdatedAt: time.Now(),
	})
}

// checkHTTP probes a URL with a GET request and returns a HealthCheck.
func checkHTTP(name, url string, timeout time.Duration) HealthCheck {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return HealthCheck{Name: name, Status: HealthCheckError, Message: fmt.Sprintf("cannot reach %s: %v", url, err)}
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return HealthCheck{Name: name, Status: HealthCheckError, Message: fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode)}
	}
	return HealthCheck{Name: name, Status: HealthCheckOK, Message: fmt.Sprintf("%s is reachable (HTTP %d)", url, resp.StatusCode)}
}

// checkHTTPWithAuth probes a URL with optional Basic Auth.
func checkHTTPWithAuth(name, url, user, pass string, timeout time.Duration) HealthCheck {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return HealthCheck{Name: name, Status: HealthCheckError, Message: fmt.Sprintf("build request: %v", err)}
	}
	if user != "" && pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return HealthCheck{Name: name, Status: HealthCheckError, Message: fmt.Sprintf("cannot reach %s: %v", url, err)}
	}
	resp.Body.Close()
	// Registry /v2/ returns 200 (authenticated) or 401 (auth required but reachable).
	if resp.StatusCode == 200 || resp.StatusCode == 401 {
		if resp.StatusCode == 200 {
			return HealthCheck{Name: name, Status: HealthCheckOK, Message: fmt.Sprintf("registry at %s is reachable and authenticated", url)}
		}
		return HealthCheck{Name: name, Status: HealthCheckWarning, Message: fmt.Sprintf("registry at %s is reachable but authentication failed (HTTP 401)", url)}
	}
	if resp.StatusCode >= 500 {
		return HealthCheck{Name: name, Status: HealthCheckError, Message: fmt.Sprintf("registry returned HTTP %d", resp.StatusCode)}
	}
	return HealthCheck{Name: name, Status: HealthCheckOK, Message: fmt.Sprintf("registry at %s responded HTTP %d", url, resp.StatusCode)}
}

// checkPath verifies a filesystem path is configured and accessible.
func checkPath(name, path string) HealthCheck {
	if path == "" {
		return HealthCheck{Name: name, Status: HealthCheckWarning, Message: "path not configured"}
	}
	if _, err := os.Stat(path); err != nil {
		return HealthCheck{Name: name, Status: HealthCheckError, Message: fmt.Sprintf("cannot access %s: %v", path, err)}
	}
	// Try to write a probe file to verify write access.
	probeFile := path + "/.muvee_health_probe"
	f, err := os.Create(probeFile)
	if err != nil {
		return HealthCheck{Name: name, Status: HealthCheckWarning, Message: fmt.Sprintf("%s is readable but not writable: %v", path, err)}
	}
	f.Close()
	os.Remove(probeFile)
	return HealthCheck{Name: name, Status: HealthCheckOK, Message: fmt.Sprintf("%s is accessible and writable", path)}
}

// checkAgents verifies that at least one agent of the given role (builder/deploy)
// has checked in within the last two minutes.
func (s *Server) checkAgents(ctx context.Context, role string) HealthCheck {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return HealthCheck{Name: role + "_agents", Status: HealthCheckError, Message: fmt.Sprintf("failed to query nodes: %v", err)}
	}
	online := 0
	cutoff := time.Now().Add(-2 * time.Minute)
	for _, n := range nodes {
		if string(n.Role) == role && n.LastSeenAt.After(cutoff) {
			online++
		}
	}
	if online == 0 {
		return HealthCheck{Name: role + "_agents", Status: HealthCheckError, Message: fmt.Sprintf("no online %s agents — start an agent with NODE_ROLE=%s", role, role)}
	}
	return HealthCheck{Name: role + "_agents", Status: HealthCheckOK, Message: fmt.Sprintf("%d %s agent(s) online", online, role)}
}

// handleAgentHealthReport accepts a health-check report from an agent and
// stores it on the node record so admins can inspect it.
func (s *Server) handleAgentHealthReport(w http.ResponseWriter, r *http.Request) {
	nodeIDStr := r.URL.Query().Get("node_id")
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid node_id"), http.StatusBadRequest)
		return
	}

	// Accept any JSON payload from the agent — store it verbatim.
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		jsonErr(w, err, http.StatusBadRequest)
		return
	}

	if err := s.store.SetNodeHealthReport(r.Context(), nodeID, []byte(raw)); err != nil {
		jsonErr(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
