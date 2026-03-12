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
	"github.com/hoveychen/muvee/internal/builder"
	"github.com/hoveychen/muvee/internal/datacache"
	"github.com/hoveychen/muvee/internal/deployer"
	"github.com/hoveychen/muvee/internal/store"
)

func main() {
	ctx := context.Background()

	nodeRole := os.Getenv("NODE_ROLE")
	if nodeRole == "" {
		log.Fatal("NODE_ROLE must be 'builder' or 'deploy'")
	}
	controlPlaneURL := os.Getenv("CONTROL_PLANE_URL")
	if controlPlaneURL == "" {
		controlPlaneURL = "http://localhost:8080"
	}
	hostname, _ := os.Hostname()
	maxStorage := int64(100 * 1024 * 1024 * 1024) // 100GB default
	nodeID := registerNode(ctx, controlPlaneURL, hostname, nodeRole, maxStorage)
	log.Printf("Agent registered as node %s (role=%s)", nodeID, nodeRole)

	registryAddr := os.Getenv("REGISTRY_ADDR")
	if registryAddr == "" {
		registryAddr = "localhost:5000"
	}
	baseDir := os.Getenv("DATA_DIR")
	if baseDir == "" {
		baseDir = "/muvee/data"
	}
	baseDomain := os.Getenv("BASE_DOMAIN")
	if baseDomain == "" {
		baseDomain = "localhost"
	}
	authServiceURL := os.Getenv("AUTH_SERVICE_URL")

	var cache *datacache.Cache
	if nodeRole == "deploy" {
		// Use a no-op store for cache; we don't need DB access from agent
		cache = datacache.New(nil, nodeID, baseDir)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	log.Println("Agent polling for tasks...")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tasks := pollTasks(ctx, controlPlaneURL, nodeID)
			for _, task := range tasks {
				go handleTask(ctx, task, controlPlaneURL, nodeID, nodeRole, registryAddr, baseDir, baseDomain, authServiceURL, cache)
			}
		}
	}
}

func registerNode(ctx context.Context, baseURL, hostname, role string, maxStorage int64) uuid.UUID {
	body, _ := json.Marshal(store.Node{
		Hostname:        hostname,
		Role:            store.NodeRole(role),
		MaxStorageBytes: maxStorage,
	})
	for {
		resp, err := http.Post(baseURL+"/api/agent/register", "application/json",
			jsonReader(body))
		if err == nil && resp.StatusCode == 200 {
			var node store.Node
			_ = json.NewDecoder(resp.Body).Decode(&node)
			resp.Body.Close()
			return node.ID
		}
		log.Printf("register failed, retrying in 5s: %v", err)
		time.Sleep(5 * time.Second)
	}
}

func pollTasks(ctx context.Context, baseURL string, nodeID uuid.UUID) []*store.Task {
	resp, err := http.Get(fmt.Sprintf("%s/api/agent/tasks?node_id=%s", baseURL, nodeID))
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	var tasks []*store.Task
	_ = json.NewDecoder(resp.Body).Decode(&tasks)
	return tasks
}

func handleTask(ctx context.Context, task *store.Task, baseURL string, nodeID uuid.UUID, role, registryAddr, baseDir, baseDomain, authServiceURL string, cache *datacache.Cache) {
	log.Printf("Handling task %s (type=%s)", task.ID, task.Type)
	markRunning(ctx, baseURL, task.ID)

	var resultErr error
	var result map[string]interface{}

	switch task.Type {
	case store.TaskTypeBuild:
		result, resultErr = runBuild(ctx, task, registryAddr, func(line string) {
			appendLog(ctx, baseURL, task.DeploymentID, line)
		})
	case store.TaskTypeDeploy:
		resultErr = runDeploy(ctx, task, cache, baseDomain, authServiceURL, func(line string) {
			appendLog(ctx, baseURL, task.DeploymentID, line)
		})
	}

	status := store.TaskStatusCompleted
	resultStr := ""
	if resultErr != nil {
		status = store.TaskStatusFailed
		resultStr = resultErr.Error()
	} else if result != nil {
		b, _ := json.Marshal(result)
		resultStr = string(b)
	}
	completeTask(ctx, baseURL, task.ID, string(status), resultStr, "")
	if result != nil {
		if imageTag, ok := result["image_tag"].(string); ok {
			completeTask(ctx, baseURL, task.ID, string(status), resultStr, imageTag)
		}
	}
}

func runBuild(ctx context.Context, task *store.Task, registryAddr string, logFn func(string)) (map[string]interface{}, error) {
	p := task.Payload
	cfg := builder.BuildConfig{
		GitURL:         str(p, "git_url"),
		GitBranch:      str(p, "git_branch"),
		DockerfilePath: str(p, "dockerfile_path"),
		DeploymentID:   str(p, "deployment_id"),
		ProjectID:      str(p, "project_id"),
		RegistryAddr:   registryAddr,
	}
	imageTag, err := builder.Build(ctx, cfg, logFn)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"image_tag": imageTag}, nil
}

func runDeploy(ctx context.Context, task *store.Task, cache *datacache.Cache, baseDomain, authServiceURL string, logFn func(string)) error {
	p := task.Payload
	var datasets []deployer.DatasetSpec
	if dsRaw, ok := p["datasets"].([]interface{}); ok {
		for _, d := range dsRaw {
			dm, _ := d.(map[string]interface{})
			if dm == nil {
				continue
			}
			datasets = append(datasets, deployer.DatasetSpec{
				ID:        str(dm, "id"),
				Name:      str(dm, "name"),
				NFSPath:   str(dm, "nfs_path"),
				Version:   int64Val(dm, "version"),
				SizeBytes: int64Val(dm, "size_bytes"),
				MountMode: str(dm, "mount_mode"),
			})
		}
	}
	cfg := deployer.Config{
		DeploymentID:   str(p, "deployment_id"),
		ProjectID:      str(p, "project_id"),
		DomainPrefix:   str(p, "domain_prefix"),
		ImageTag:       str(p, "image_tag"),
		AuthRequired:   boolVal(p, "auth_required"),
		AuthDomains:    str(p, "auth_domains"),
		Datasets:       datasets,
		BaseDomain:     baseDomain,
		AuthServiceURL: authServiceURL,
	}
	return deployer.Deploy(ctx, cfg, cache, nil, logFn)
}

func markRunning(ctx context.Context, baseURL string, taskID uuid.UUID) {
	body, _ := json.Marshal(map[string]string{"status": "running"})
	resp, _ := http.Post(fmt.Sprintf("%s/api/agent/tasks/%s/complete", baseURL, taskID),
		"application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

func completeTask(ctx context.Context, baseURL string, taskID uuid.UUID, status, result, imageTag string) {
	body, _ := json.Marshal(map[string]string{"status": status, "result": result, "image_tag": imageTag})
	resp, _ := http.Post(fmt.Sprintf("%s/api/agent/tasks/%s/complete", baseURL, taskID),
		"application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

func appendLog(ctx context.Context, baseURL string, deploymentID uuid.UUID, line string) {
	body, _ := json.Marshal(map[string]string{"line": line})
	resp, _ := http.Post(fmt.Sprintf("%s/api/deployments/%s/logs", baseURL, deploymentID),
		"application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

func str(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func int64Val(m map[string]interface{}, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func boolVal(m map[string]interface{}, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func jsonReader(b []byte) *jsonBuf { return &jsonBuf{b: b} }

type jsonBuf struct {
	b   []byte
	pos int
}

func (j *jsonBuf) Read(p []byte) (int, error) {
	n := copy(p, j.b[j.pos:])
	j.pos += n
	if j.pos >= len(j.b) {
		return n, fmt.Errorf("EOF")
	}
	return n, nil
}
