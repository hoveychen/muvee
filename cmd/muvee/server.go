package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/api"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/monitor"
	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
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
		log.Println("Warning: AGENT_SECRET is not set; agent endpoints are unauthenticated")
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
	})
	handler := srv.TunnelAwareHandler(mountFrontend(srv.Router()))

	go processBuildCompletions(ctx, st, sched)
	go processNodeFailovers(ctx, st, sched)

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
			continue
		}
		// Node is considered dead. Evict all its running deployments.
		deps, err := st.GetRunningDeploymentsByNode(ctx, node.ID)
		if err != nil || len(deps) == 0 {
			continue
		}
		log.Printf("Node %s (%s) has been offline for %s; evicting %d deployment(s)",
			node.Hostname, node.ID, time.Since(node.LastSeenAt).Round(time.Second), len(deps))
		for _, dep := range deps {
			project, err := st.GetProject(ctx, dep.ProjectID)
			if err != nil || project == nil {
				continue
			}
			if dep.ImageTag == "" {
				log.Printf("Skipping eviction of deployment %s: no image tag", dep.ID)
				continue
			}
			// Mark old deployment as stopped before creating the replacement.
			_ = st.UpdateDeploymentStatus(ctx, dep.ID, store.DeploymentStatusStopped,
				fmt.Sprintf("evicted: node %s (%s) offline", node.Hostname, node.ID))

			newDep, err := st.CreateDeployment(ctx, &store.Deployment{
				ProjectID: dep.ProjectID,
				ImageTag:  dep.ImageTag,
				CommitSHA: dep.CommitSHA,
			})
			if err != nil {
				log.Printf("Failed to create replacement deployment for project %s: %v", project.Name, err)
				continue
			}
			_ = st.UpdateDeploymentStatus(ctx, newDep.ID, store.DeploymentStatusDeploying, "")
			if err := sched.DispatchDeploy(ctx, newDep, project, newDep.ImageTag); err != nil {
				log.Printf("Failed to dispatch failover deploy for project %s: %v", project.Name, err)
				_ = st.UpdateDeploymentStatus(ctx, newDep.ID, store.DeploymentStatusFailed, err.Error())
			} else {
				log.Printf("Failover deployment %s dispatched for project %s", newDep.ID, project.Name)
			}
		}
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
		_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusDeploying, "")
		if err := sched.DispatchDeploy(ctx, deployment, project, res.ImageTag); err != nil {
			fmt.Printf("dispatch deploy error: %v\n", err)
			_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusFailed, err.Error())
		}
		_ = st.UpdateTaskStatus(ctx, r.taskID, store.TaskStatusCompleted, r.result+"_dispatched")
	}
}
