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

	handler := consoleHandler(srv.TunnelAwareHandler, srv.Router())
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

// consoleHandler assembles the server's root handler. Order matters:
// tunnel / domain_only Hosts are routed away first (tenant traffic keeps the
// tenant's headers); everything else — the console SPA included, which
// mountFrontend serves without going through Router — gets the anti-framing
// headers (BACKLOG-20261124-001).
func consoleHandler(tunnelAware func(http.Handler) http.Handler, router http.Handler) http.Handler {
	return tunnelAware(api.AntiFramingHandler(mountFrontend(router)))
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
// deployment is only retired once its replacement has completed (see
// failoverDeployment); until then — and whenever no replacement can be placed
// at all — the original row stays 'running' and keeps its Traefik route
// (BACKLOG-20261124-002: a single-node stack lost every route for 15 days
// because the old rows were stopped first and the replacement then failed
// with "no active deploy nodes").
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
// at all, or "" if failover may try. A project fixed to the dead node cannot
// move. A compose/image project keeps its named volumes on the node it runs
// on, so it only moves when an admin explicitly fixed it (fixed_node_id) to
// another node: pinned to the dead node, or neither pinned nor fixed (e.g.
// fixed_node_id was cleared without a redeploy), it would come up on empty
// volumes elsewhere. Anything less predictable is left to the dispatch
// attempt, whose failure is counted by the failover backoff.
func failoverBlocker(project *store.Project, deadNode uuid.UUID) string {
	if project.FixedNodeID != nil && *project.FixedNodeID == deadNode {
		return "project is fixed (fixed_node_id) to the offline node"
	}
	if project.ProjectType != store.ProjectTypeCompose && project.ProjectType != store.ProjectTypeImage {
		return ""
	}
	if project.FixedNodeID != nil {
		return ""
	}
	if project.PinnedNodeID != nil && *project.PinnedNodeID == deadNode {
		return "compose/image project is pinned to the offline node (its named volumes live there); " +
			"to move it anyway, set its fixed_node_id to another node (it starts there with empty volumes unless you copy them)"
	}
	return "compose/image project is not fixed to another node (no fixed_node_id) and its named volumes live on the offline node; " +
		"to move it anyway, set its fixed_node_id to another node (it starts there with empty volumes unless you copy them)"
}

// failoverBackoff: a replacement that keeps failing on the healthy node is
// retried after 5m, 10m, 20m, ... and given up after 8 failed attempts in one
// outage, so a deterministic failure costs at most 8 failed rows instead of
// one per one-minute tick.
var failoverBackoff = store.FailoverBackoff{Base: 5 * time.Minute, MaxAttempts: 8}

// failoverDeployment queues a replacement for dep through the project's deploy
// queue and lets the queue dispatch it. dep itself is left alone — 'running'
// and routed — until the replacement's deploy completes; the completion
// callback (scheduler.RetireReplacedDeployments) then stops it and queues a
// cleanup on the dead node. So a replacement that fails on the healthy node
// leaves the project routed exactly as before, the orphan container is
// removed when the node returns, and a user deployment is never raced: the
// queue runs one deployment of a project at a time and a user deployment
// always supersedes a queued replacement.
//
// If dispatching fails outright, the replacement row is kept as 'failed' with
// the reason: the backoff counts failed replacements, so a deterministic
// dispatch failure costs one row per backoff window and at most MaxAttempts
// rows per outage instead of an insert/fail/delete cycle every tick.
func failoverDeployment(ctx context.Context, st *store.Store, sched *scheduler.Scheduler, node *store.Node, dep *store.Deployment, project *store.Project) {
	// Let the deploy queue reap what is presumed lost first: a deploy that was
	// dispatched to this node just before it died stays in flight forever
	// otherwise (the queue sweeper only visits projects with queued rows) and
	// would block the failover for the whole outage.
	_, _ = sched.AdvanceDeployQueue(ctx, project.ID)
	res, err := st.EnqueueFailoverReplacement(ctx, dep, node, failoverBackoff)
	if err != nil {
		log.Printf("Failover of project %s: could not queue a replacement for deployment %s: %v", project.Name, dep.ID, err)
		return
	}
	if res.Retired != nil {
		log.Printf("Failover of project %s: deployment %s on offline node %s retired, newer deployment %s already runs",
			project.Name, dep.ID, node.Hostname, res.RetiredFor.ID)
		sched.CleanUpRetiredDeployment(ctx, res.Retired, res.RetiredFor.NodeID, project)
		return
	}
	if res.Replacement == nil {
		key := fmt.Sprintf("skip:%s:%d:%v", dep.ID, res.Failures, res.GaveUp)
		if res.GaveUp {
			if failoverSkipLog.first(node.ID, key) {
				log.Printf("WARNING: failover of project %s off offline node %s: %s; deployment %s stays running and routed "+
					"(redeploy the project, or bring the node back)", project.Name, node.Hostname, res.Skip, dep.ID)
			}
		} else if failoverSkipLog.first(node.ID, key) {
			log.Printf("Failover of project %s off offline node %s: not queuing a replacement for deployment %s: %s",
				project.Name, node.Hostname, dep.ID, res.Skip)
		}
		return
	}
	repl := res.Replacement
	dispatched, derr := sched.AdvanceDeployQueue(ctx, project.ID)
	switch {
	case dispatched == repl.ID && derr != nil:
		// AdvanceDeployQueue already marked it failed with derr; say what it
		// means for the project (and make sure it is failed, so it counts for
		// the backoff).
		note := fmt.Sprintf("failover: dispatching this replacement failed: %v; deployment %s stays running and routed on offline node %s; "+
			"failover retries with backoff", derr, dep.ID, node.Hostname)
		if err := st.UpdateDeploymentStatus(ctx, repl.ID, store.DeploymentStatusFailed, note); err != nil {
			log.Printf("Failover of project %s: could not mark replacement deployment %s failed: %v", project.Name, repl.ID, err)
		}
		if failoverSkipLog.first(node.ID, "dispatch-failed:"+dep.ID.String()) {
			log.Printf("WARNING: failover of project %s off offline node %s: dispatching replacement %s failed: %v; "+
				"deployment %s left running and routed, retrying with backoff", project.Name, node.Hostname, repl.ID, derr, dep.ID)
		}
		// Let anything that queued behind the replacement run now.
		_, _ = sched.AdvanceDeployQueue(ctx, project.ID)
	case dispatched == repl.ID:
		log.Printf("Failover deployment %s dispatched for project %s (replaces %s on offline node %s)", repl.ID, project.Name, dep.ID, node.Hostname)
	default:
		log.Printf("Failover deployment %s queued for project %s (replaces %s on offline node %s)", repl.ID, project.Name, dep.ID, node.Hostname)
	}
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
