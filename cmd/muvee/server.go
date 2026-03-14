package main

import (
	"context"
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

	st := store.New(db)
	authSvc, err := auth.New(st)
	if err != nil {
		log.Fatalf("auth service: %v", err)
	}
	sched := scheduler.New(st)
	scanInterval := 5 * time.Minute
	mon := monitor.New(st, scanInterval, 4)
	go mon.Start(ctx)

	baseDomain := os.Getenv("BASE_DOMAIN")
	if baseDomain == "" {
		baseDomain = "localhost"
	}
	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	agentSecret := os.Getenv("AGENT_SECRET")
	if agentSecret == "" {
		log.Println("Warning: AGENT_SECRET is not set; agent endpoints are unauthenticated")
	}
	registryAddr := os.Getenv("REGISTRY_ADDR")
	if registryAddr == "" {
		registryAddr = "localhost:5000"
	}

	srv := api.NewServer(st, authSvc, sched, mon, api.ServerConfig{
		BaseDomain:       baseDomain,
		AuthServiceURL:   authServiceURL,
		AgentSecret:      agentSecret,
		RegistryAddr:     registryAddr,
		RegistryUser:     os.Getenv("REGISTRY_USER"),
		RegistryPassword: os.Getenv("REGISTRY_PASSWORD"),
	})
	handler := mountFrontend(srv.Router())

	go processBuildCompletions(ctx, st, sched)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
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
		frontend.ServeHTTP(w, r)
	})
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
		var res struct{ ImageTag string `json:"image_tag"` }
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
		_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusDeploying, "")
		if err := sched.DispatchDeploy(ctx, deployment, project, res.ImageTag); err != nil {
			fmt.Printf("dispatch deploy error: %v\n", err)
			_ = st.UpdateDeploymentStatus(ctx, r.deploymentID, store.DeploymentStatusFailed, err.Error())
		}
		_ = st.UpdateTaskStatus(ctx, r.taskID, store.TaskStatusCompleted, r.result+"_dispatched")
	}
}
