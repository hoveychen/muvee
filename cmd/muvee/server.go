package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/api"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/domains"
	"github.com/hoveychen/muvee/internal/monitor"
	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
	"github.com/hoveychen/muvee/internal/traefiklog"
	webui "github.com/hoveychen/muvee/web/embed"
)

func runServer() {
	ctx := context.Background()

	db, err := store.Connect(ctx)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	migrationsDir := os.Getenv("MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "./db/migrations"
	}
	if err := store.Migrate(ctx, db, migrationsDir); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("Migrations applied.")

	var encryptionKey []byte
	if keyHex := os.Getenv("SECRET_ENCRYPTION_KEY"); keyHex != "" {
		key, err := hex.DecodeString(keyHex)
		if err != nil || len(key) != 32 {
			log.Fatalf("SECRET_ENCRYPTION_KEY must be a 64-character hex string (32 bytes); got error: %v", err)
		}
		encryptionKey = key
		log.Println("Secret encryption enabled.")
	} else {
		log.Println("Warning: SECRET_ENCRYPTION_KEY is not set; secrets feature is disabled")
	}
	st := store.NewWithEncryption(db, encryptionKey)
	authSvc, err := auth.New(st)
	if err != nil {
		log.Fatalf("auth service: %v", err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	agentSecret := os.Getenv("AGENT_SECRET")
	if agentSecret == "" {
		log.Fatal("AGENT_SECRET environment variable is required (was empty)")
	}
	if len(agentSecret) < 32 {
		log.Printf("Warning: AGENT_SECRET is %d bytes (strongly recommended >= 32)", len(agentSecret))
	}

	sched := scheduler.New(st)
	sched.SetGitHostingConfig(agentSecret)

	scanInterval := 5 * time.Minute
	datasetNFSBasePath := os.Getenv("DATASET_NFS_BASE_PATH")
	mon := monitor.New(st, datasetNFSBasePath, scanInterval, 4)
	go mon.Start(ctx)

	baseDomain := os.Getenv("BASE_DOMAIN")
	if baseDomain == "" {
		baseDomain = "localhost"
	}
	// BASE_DOMAINS (comma-separated) lets the same instance be served under more
	// than one apex. baseDomain stays the canonical default (first entry).
	baseDomains := domains.Parse(baseDomain, os.Getenv("BASE_DOMAINS"))
	if len(baseDomains) > 1 {
		log.Printf("Serving multiple base domains: %s", strings.Join(baseDomains, ", "))
	}
	// CF_TUNNEL_DOMAINS marks some of those base domains as reached through a
	// Cloudflare Tunnel: Traefik serves them on the internal plain-HTTP
	// cftunnel entrypoint and never requests ACME certs for them.
	cfTunnelDomains, err := domains.ParseSubset(os.Getenv("CF_TUNNEL_DOMAINS"), baseDomains)
	if err != nil {
		log.Fatalf("CF_TUNNEL_DOMAINS: %v", err)
	}
	if len(cfTunnelDomains) > 0 {
		log.Printf("Cloudflare Tunnel base domains: %s", strings.Join(cfTunnelDomains, ", "))
	}
	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	registryAddr := os.Getenv("REGISTRY_ADDR")
	if registryAddr == "" {
		registryAddr = "localhost:5000"
	}

	volumeNFSBasePath := os.Getenv("VOLUME_NFS_BASE_PATH")
	if volumeNFSBasePath != "" {
		log.Printf("Workspace volume base path: %s", volumeNFSBasePath)
	} else {
		log.Println("Warning: VOLUME_NFS_BASE_PATH is not set; project workspace volumes are disabled")
	}
	if datasetNFSBasePath != "" {
		log.Printf("Dataset NFS base path: %s", datasetNFSBasePath)
	} else {
		log.Println("Warning: DATASET_NFS_BASE_PATH is not set; dataset feature is disabled")
	}

	gitRepoBasePath := os.Getenv("GIT_REPO_BASE_PATH")
	if gitRepoBasePath != "" {
		log.Printf("Git repository base path: %s", gitRepoBasePath)
	} else {
		log.Println("Warning: GIT_REPO_BASE_PATH is not set; hosted git repositories are disabled")
	}

	brandingDir := os.Getenv("BRANDING_DIR")
	if brandingDir == "" {
		brandingDir = "/data/branding"
	}

	tunnelBackendURL := os.Getenv("TUNNEL_BACKEND_URL")
	if tunnelBackendURL != "" {
		log.Printf("Tunnel backend URL: %s", tunnelBackendURL)
	}

	srv := api.NewServer(st, authSvc, sched, mon, api.ServerConfig{
		BaseDomain:         baseDomain,
		BaseDomains:        baseDomains,
		CFTunnelDomains:    cfTunnelDomains,
		AuthServiceURL:     authServiceURL,
		AgentSecret:        agentSecret,
		RegistryAddr:       registryAddr,
		RegistryUser:       os.Getenv("REGISTRY_USER"),
		RegistryPassword:   os.Getenv("REGISTRY_PASSWORD"),
		VolumeNFSBasePath:  volumeNFSBasePath,
		DatasetNFSBasePath: datasetNFSBasePath,
		GitRepoBasePath:    gitRepoBasePath,
		BrandingDir:        brandingDir,
		TunnelBackendURL:   tunnelBackendURL,
		ACMEStoragePath:    os.Getenv("ACME_STORAGE_PATH"),
		ServerVersion:      version,
	})
	// Providers that inline their avatar instead of returning a URL (Entra,
	// whose ID tokens have no `picture` claim) hand over a base64 data URI.
	// Route every identity write through the server's materialiser so what
	// lands in the database — and in the forward-auth session cookie — is an
	// ordinary URL. Installed here because the implementation needs the asset
	// directory and base domain that only the API server knows.
	authSvc.SetAvatarMaterializer(srv.MaterializeAvatar)

	handler := srv.TunnelAwareHandler(mountFrontend(srv.Router()))
	srv.StartBackgroundWorkers(ctx)

	sched.SetImageWatchConfig(gitRepoBasePath, registryAddr, os.Getenv("REGISTRY_USER"), os.Getenv("REGISTRY_PASSWORD"))

	go processBuildCompletions(ctx, st, sched)
	go processNodeFailovers(ctx, st, sched)
	sched.StartAutoDeployPoller(ctx)
	sched.StartDeployQueueSweeper(ctx, 30*time.Second)
	sched.StartImageWatcher(ctx)

	if p := os.Getenv("TRAEFIK_ACCESS_LOG_PATH"); p != "" {
		go traefiklog.New(st, p, baseDomain).Start(ctx)
	}

	log.Printf("muvee server listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// mountFrontend wraps the API router so that non-API/auth paths serve the embedded React app.
func mountFrontend(apiHandler http.Handler) http.Handler {
	frontend := webui.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if len(p) >= 4 && p[:4] == "/api" {
			apiHandler.ServeHTTP(w, r)
			return
		}
		if len(p) >= 5 && p[:5] == "/auth" {
			apiHandler.ServeHTTP(w, r)
			return
		}
		if len(p) >= 5 && p[:5] == "/git/" {
			apiHandler.ServeHTTP(w, r)
			return
		}
		frontend.ServeHTTP(w, r)
	})
}

// processNodeFailovers periodically detects dead deploy nodes and re-dispatches
// their running deployments onto healthy nodes.
func processNodeFailovers(ctx context.Context, st *store.Store, sched *scheduler.Scheduler) {
	const deadNodeThreshold = 3 * time.Minute
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkNodeFailovers(ctx, st, sched, deadNodeThreshold)
		}
	}
}

// deployNodeActiveWindow mirrors the liveness window scheduler.PickDeployNode,
// resolveFixedDeployNode and pickOrReusePinnedNode use to accept a deploy node.
const deployNodeActiveWindow = 2 * time.Minute

// failoverSkipLog makes the "cannot fail over" warnings fire once per node
// outage instead of on every one-minute tick. Entries are dropped as soon as
// the node is seen alive again, so the next outage warns afresh.
var failoverSkipLog = &nodeLogOnce{seen: map[uuid.UUID]map[string]bool{}}

type nodeLogOnce struct {
	mu   sync.Mutex
	seen map[uuid.UUID]map[string]bool
}

func (l *nodeLogOnce) first(nodeID uuid.UUID, key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	keys := l.seen[nodeID]
	if keys == nil {
		keys = map[string]bool{}
		l.seen[nodeID] = keys
	}
	if keys[key] {
		return false
	}
	keys[key] = true
	return true
}

func (l *nodeLogOnce) reset(nodeID uuid.UUID) {
	l.mu.Lock()
	delete(l.seen, nodeID)
	l.mu.Unlock()
}

// checkNodeFailovers moves running deployments off deploy nodes whose agent
// has not heartbeated for threshold.
//
// An offline agent does not mean the containers are down (the agent may have
// crashed, or lost its control-plane link, while docker keeps serving). So a
// deployment is only retired once a replacement has actually been dispatched;
// whenever that is impossible the original row stays 'running' and keeps its
// Traefik route (BACKLOG-20261124-002: a single-node stack lost every route
// for 15 days because the old rows were stopped first and the replacement then
// failed with "no active deploy nodes").
func checkNodeFailovers(ctx context.Context, st *store.Store, sched *scheduler.Scheduler, threshold time.Duration) {
	nodes, err := st.ListNodes(ctx)
	if err != nil {
		return
	}
	for _, node := range nodes {
		if node.Role != store.NodeRoleDeploy {
			continue
		}
		if time.Since(node.LastSeenAt) < threshold {
			failoverSkipLog.reset(node.ID)
			continue
		}
		// Node is considered dead. Evict all its running deployments.
		deps, err := st.GetRunningDeploymentsByNode(ctx, node.ID)
		if err != nil || len(deps) == 0 {
			continue
		}
		offlineFor := time.Since(node.LastSeenAt).Round(time.Second)
		// Without another live deploy node no replacement can be placed
		// anywhere. Skip up front rather than create-and-roll-back a
		// replacement row for every deployment on every tick of the outage.
		if !hasOtherActiveDeployNode(nodes, node.ID) {
			if failoverSkipLog.first(node.ID, "no-other-node") {
				log.Printf("WARNING: node %s (%s) has been offline for %s, but there is no other active deploy node: "+
					"NOT evicting its %d running deployment(s); they stay running and routed until the agent returns",
					node.Hostname, node.ID, offlineFor, len(deps))
			}
			continue
		}
		for _, dep := range deps {
			project, err := st.GetProject(ctx, dep.ProjectID)
			if err != nil || project == nil {
				continue
			}
			// A paused project keeps its deployment row on 'running' (that is how
			// resume finds it again), so it still shows up here — but it must not
			// be resurrected: re-dispatching it would start the container the
			// admin deliberately stopped.
			if project.Paused {
				log.Printf("Skipping eviction of deployment %s: project %s is paused", dep.ID, project.Name)
				continue
			}
			if dep.ImageTag == "" {
				log.Printf("Skipping eviction of deployment %s: no image tag", dep.ID)
				continue
			}
			if reason := failoverBlocker(project, node.ID); reason != "" {
				if failoverSkipLog.first(node.ID, dep.ID.String()) {
					log.Printf("WARNING: not evicting deployment %s of project %s from offline node %s (%s): %s; it stays running and routed",
						dep.ID, project.Name, node.Hostname, node.ID, reason)
				}
				continue
			}
			log.Printf("Node %s (%s) has been offline for %s; evicting deployment %s of project %s",
				node.Hostname, node.ID, offlineFor, dep.ID, project.Name)
			failoverDeployment(ctx, st, sched, node, dep, project)
		}
	}
}

// hasOtherActiveDeployNode reports whether any deploy node other than exclude
// is live enough for the scheduler to place a deployment on it.
func hasOtherActiveDeployNode(nodes []*store.Node, exclude uuid.UUID) bool {
	for _, n := range nodes {
		if n.Role == store.NodeRoleDeploy && n.ID != exclude && time.Since(n.LastSeenAt) < deployNodeActiveWindow {
			return true
		}
	}
	return false
}

// failoverBlocker returns why the project's deployment cannot leave deadNode
// at all, or "" if failover may try. These mirror the scheduler's refusals
// (resolveFixedDeployNode, pickOrReusePinnedNode) that are certain when the
// binding points at the dead node itself; anything less predictable is left to
// the dispatch attempt and its rollback.
func failoverBlocker(project *store.Project, deadNode uuid.UUID) string {
	if project.FixedNodeID != nil {
		if *project.FixedNodeID == deadNode {
			return "project is fixed (fixed_node_id) to the offline node"
		}
		return ""
	}
	if (project.ProjectType == store.ProjectTypeCompose || project.ProjectType == store.ProjectTypeImage) &&
		project.PinnedNodeID != nil && *project.PinnedNodeID == deadNode {
		return "compose/image project is pinned to the offline node (its volumes live there)"
	}
	return ""
}

// failoverDeployment dispatches a replacement for dep and only then retires
// dep. If dispatching fails, the replacement row is deleted (it never ran, and
// a leftover 'failed' row would become the project's newest deployment and
// show the project as failed) and dep is left exactly as it was — still
// 'running', still routed, with no eviction note — so nothing has to be
// repaired by hand once the node comes back.
func failoverDeployment(ctx context.Context, st *store.Store, sched *scheduler.Scheduler, node *store.Node, dep *store.Deployment, project *store.Project) {
	newDep, err := st.CreateDeployment(ctx, &store.Deployment{
		ProjectID: dep.ProjectID,
		ImageTag:  dep.ImageTag,
		CommitSHA: dep.CommitSHA,
	})
	if err != nil {
		log.Printf("Failed to create replacement deployment for project %s: %v", project.Name, err)
		return
	}
	_ = st.UpdateDeploymentStatus(ctx, newDep.ID, store.DeploymentStatusDeploying, "")
	if err := sched.DispatchDeploy(ctx, newDep, project, newDep.ImageTag); err != nil {
		if derr := st.DeleteDeployment(ctx, newDep.ID); derr != nil {
			log.Printf("Failover of project %s: could not roll back replacement deployment %s: %v", project.Name, newDep.ID, derr)
			_ = st.UpdateDeploymentStatus(ctx, newDep.ID, store.DeploymentStatusFailed, err.Error())
		}
		log.Printf("WARNING: failover of project %s off offline node %s aborted: dispatching the replacement failed: %v; "+
			"deployment %s left running and routed, replacement rolled back", project.Name, node.Hostname, err, dep.ID)
		// The replacement was briefly in flight and may have made a concurrent
		// deploy queue behind it; let that one run now.
		_, _ = sched.AdvanceDeployQueue(ctx, project.ID)
		return
	}
	// The replacement is on its way: retire the old row now, so the dead
	// node's address leaves the route set and the next tick does not evict
	// the same deployment again.
	_ = st.UpdateDeploymentStatus(ctx, dep.ID, store.DeploymentStatusStopped,
		fmt.Sprintf("evicted: node %s (%s) offline", node.Hostname, node.ID))
	log.Printf("Failover deployment %s dispatched for project %s", newDep.ID, project.Name)
}

// processBuildCompletions polls for completed build tasks and dispatches deploy tasks.
func processBuildCompletions(ctx context.Context, st *store.Store, sched *scheduler.Scheduler) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkBuildCompletions(ctx, st, sched)
		}
	}
}

func checkBuildCompletions(ctx context.Context, st *store.Store, sched *scheduler.Scheduler) {
	rows, err := st.DB().Query(ctx, `
		SELECT t.id, t.deployment_id, t.result FROM tasks t
		JOIN deployments d ON d.id = t.deployment_id
		WHERE t.type = 'build' AND t.status = 'completed' AND d.status = 'building'
		LIMIT 20
	`)
	if err != nil {
		return
	}
	defer rows.Close()
	type row struct {
		taskID       uuid.UUID
		deploymentID uuid.UUID
		result       string
	}
	var rows2 []row
	for rows.Next() {
		var r row
		_ = rows.Scan(&r.taskID, &r.deploymentID, &r.result)
		rows2 = append(rows2, r)
	}
	for _, r := range rows2 {
		var res struct {
			ImageTag string `json:"image_tag"`
		}
		_ = json.Unmarshal([]byte(r.result), &res)
		if res.ImageTag == "" {
			continue
		}
		deployment, err := st.GetDeployment(ctx, r.deploymentID)
		if err != nil || deployment == nil {
			continue
		}
		project, err := st.GetProject(ctx, deployment.ProjectID)
		if err != nil || project == nil {
			continue
		}
		_ = st.SetDeploymentImageTag(ctx, r.deploymentID, res.ImageTag)
		// Build-only projects stop here: no container start, no host port, no
		// Traefik route. The image tag is recorded on the project for
		// downstream compose/image projects to consume, and any opted-in
		// downstream projects (triggers_redeploy_of) are kicked off as
		// independent deployments.
		if project.ProjectType == store.ProjectTypeBuild {
			_ = st.SetProjectLastImageTag(ctx, project.ID, res.ImageTag)
			_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusRunning, "")
			_ = st.SetDeploymentHostPort(ctx, r.deploymentID, 0)
			_ = st.UpdateTaskStatus(ctx, r.taskID, store.TaskStatusCompleted, r.result+"_dispatched")
			_, _ = sched.AdvanceDeployQueue(ctx, project.ID)
			autoTriggerDownstreamRedeploys(ctx, st, sched, project)
			continue
		}
		_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusDeploying, "")
		if err := sched.DispatchDeploy(ctx, deployment, project, res.ImageTag); err != nil {
			fmt.Printf("dispatch deploy error: %v\n", err)
			_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusFailed, err.Error())
			_ = st.UpdateTaskStatus(ctx, r.taskID, store.TaskStatusCompleted, r.result+"_dispatched")
			_, _ = sched.AdvanceDeployQueue(ctx, project.ID)
			continue
		}
		_ = st.UpdateTaskStatus(ctx, r.taskID, store.TaskStatusCompleted, r.result+"_dispatched")
	}
}

// autoTriggerDownstreamRedeploys parses the build project's triggers_redeploy_of
// JSON array and fires a TriggerDeployment for each listed downstream project.
// Failures are logged but do not block — the build itself is already complete,
// and a downstream that can't deploy now is a problem on its own.
func autoTriggerDownstreamRedeploys(ctx context.Context, st *store.Store, sched *scheduler.Scheduler, project *store.Project) {
	raw := project.TriggersRedeployOf
	if raw == "" || raw == "[]" {
		return
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		log.Printf("build %s: malformed triggers_redeploy_of (%q): %v", project.ID, raw, err)
		return
	}
	for _, idStr := range ids {
		downstreamID, err := uuid.Parse(idStr)
		if err != nil {
			log.Printf("build %s: triggers_redeploy_of entry %q is not a UUID: %v", project.ID, idStr, err)
			continue
		}
		if _, err := sched.TriggerDeployment(ctx, downstreamID, "build-chain"); err != nil {
			log.Printf("build %s: auto-chain redeploy of %s failed: %v", project.ID, downstreamID, err)
		} else {
			log.Printf("build %s: auto-chain redeploy of %s dispatched", project.ID, downstreamID)
		}
	}
}
