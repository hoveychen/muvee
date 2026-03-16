package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
	agentSecret := os.Getenv("AGENT_SECRET")
	if agentSecret == "" {
		log.Println("Warning: AGENT_SECRET is not set; requests to control plane are unauthenticated")
	}

	hostname, _ := os.Hostname()

	hostIP := os.Getenv("HOST_IP")
	if hostIP == "" {
		hostIP = detectOutboundIP(controlPlaneURL)
	}
	if hostIP == "" {
		log.Println("Warning: could not detect HOST_IP; deploy routes may not be reachable by Traefik")
	} else {
		log.Printf("Using HOST_IP=%s", hostIP)
	}

	maxStorage := int64(100 * 1024 * 1024 * 1024) // 100GB default
	nodeID := registerNode(ctx, controlPlaneURL, agentSecret, hostname, nodeRole, hostIP, maxStorage)
	log.Printf("Agent registered as node %s (role=%s, ip=%s)", nodeID, nodeRole, hostIP)

	agentCfg := fetchAgentConfig(ctx, controlPlaneURL, agentSecret)
	registryAddr := agentCfg["registry_addr"]
	baseDomain := agentCfg["base_domain"]

	if user, pass := agentCfg["registry_user"], agentCfg["registry_password"]; user != "" && pass != "" {
		if err := dockerLogin(registryAddr, user, pass); err != nil {
			log.Fatalf("docker login %s failed: %v", registryAddr, err)
		}
		log.Printf("Authenticated with registry %s as %s", registryAddr, user)
	}
	baseDir := os.Getenv("DATA_DIR")
	if baseDir == "" {
		baseDir = "/muvee/data"
	}

	var cache *datacache.Cache
	if nodeRole == "deploy" {
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
			tasks := pollTasks(ctx, controlPlaneURL, agentSecret, nodeID)
			for _, task := range tasks {
				go handleTask(ctx, task, controlPlaneURL, agentSecret, nodeID, nodeRole, registryAddr, baseDir, baseDomain, cache)
			}
		}
	}
}

// detectOutboundIP returns the local IP on the interface used to reach the control plane.
// Using the control plane address (rather than a public internet host) ensures the correct
// interface is selected even when the node has no internet access.
func detectOutboundIP(controlPlaneURL string) string {
	host := extractHost(controlPlaneURL)
	if host == "" {
		host = "8.8.8.8"
	}
	conn, err := net.Dial("udp", host+":80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// extractHost parses a URL and returns the hostname without scheme, port, or path.
func extractHost(rawURL string) string {
	s := rawURL
	if i := len("https://"); len(s) > i && s[:i] == "https://" {
		s = s[i:]
	} else if i := len("http://"); len(s) > i && s[:i] == "http://" {
		s = s[i:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	return s
}

func dockerLogin(registry, user, password string) error {
	cmd := exec.Command("docker", "login", registry, "-u", user, "--password-stdin")
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// fetchAgentConfig retrieves runtime configuration distributed by the control plane,
// such as registry credentials and base domain, so agents don't need local env vars.
func fetchAgentConfig(ctx context.Context, baseURL, secret string) map[string]string {
	for {
		resp, err := agentGet(baseURL+"/api/agent/config", secret)
		if err == nil && resp.StatusCode == 200 {
			var cfg map[string]string
			_ = json.NewDecoder(resp.Body).Decode(&cfg)
			resp.Body.Close()
			log.Printf("Fetched agent config from control plane (registry=%s, domain=%s)", cfg["registry_addr"], cfg["base_domain"])
			return cfg
		}
		log.Printf("fetch agent config failed, retrying in 5s: %v", err)
		time.Sleep(5 * time.Second)
	}
}

func registerNode(ctx context.Context, baseURL, secret, hostname, role, hostIP string, maxStorage int64) uuid.UUID {
	body, _ := json.Marshal(store.Node{
		Hostname:        hostname,
		Role:            store.NodeRole(role),
		HostIP:          hostIP,
		MaxStorageBytes: maxStorage,
	})
	for {
		resp, err := agentPost(baseURL+"/api/agent/register", secret, "application/json", jsonReader(body))
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

func pollTasks(ctx context.Context, baseURL, secret string, nodeID uuid.UUID) []*store.Task {
	resp, err := agentGet(fmt.Sprintf("%s/api/agent/tasks?node_id=%s", baseURL, nodeID), secret)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	var tasks []*store.Task
	_ = json.NewDecoder(resp.Body).Decode(&tasks)
	return tasks
}

func handleTask(ctx context.Context, task *store.Task, baseURL, secret string, nodeID uuid.UUID, role, registryAddr, baseDir, baseDomain string, cache *datacache.Cache) {
	log.Printf("Handling task %s (type=%s)", task.ID, task.Type)
	completeTask(ctx, baseURL, secret, task.ID, store.TaskStatusRunning, nil)

	var taskErr error
	extra := map[string]interface{}{}

	switch task.Type {
	case store.TaskTypeBuild:
		result, err := runBuild(ctx, task, registryAddr, func(line string) {
			appendLog(ctx, baseURL, secret, task.DeploymentID, line)
		})
		taskErr = err
		if err == nil && result != nil {
			if imageTag, ok := result["image_tag"].(string); ok {
				extra["image_tag"] = imageTag
			}
		}

	case store.TaskTypeDeploy:
		hostPort, err := runDeploy(ctx, task, cache, baseDomain, func(line string) {
			appendLog(ctx, baseURL, secret, task.DeploymentID, line)
		})
		taskErr = err
		if err == nil && hostPort > 0 {
			extra["host_port"] = hostPort
		}
	}

	if taskErr != nil {
		extra["result"] = taskErr.Error()
		completeTask(ctx, baseURL, secret, task.ID, store.TaskStatusFailed, extra)
	} else {
		completeTask(ctx, baseURL, secret, task.ID, store.TaskStatusCompleted, extra)
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
		SSHKey:         str(p, "git_ssh_key"),
		GitUsername:    str(p, "git_username"),
		GitToken:       str(p, "git_token"),
		BuildSecrets:   mapStrStr(p, "build_secrets"),
	}
	imageTag, err := builder.Build(ctx, cfg, logFn)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"image_tag": imageTag}, nil
}

func runDeploy(ctx context.Context, task *store.Task, cache *datacache.Cache, baseDomain string, logFn func(string)) (int, error) {
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
	// Extract env vars from payload (map[string]interface{} decoded from JSON).
	envVars := make(map[string]string)
	if ev, ok := p["env_vars"].(map[string]interface{}); ok {
		for k, v := range ev {
			if s, ok := v.(string); ok {
				envVars[k] = s
			}
		}
	}
	cfg := deployer.Config{
		DeploymentID:  str(p, "deployment_id"),
		ProjectID:     str(p, "project_id"),
		DomainPrefix:  str(p, "domain_prefix"),
		ImageTag:      str(p, "image_tag"),
		ContainerPort: intVal(p, "container_port"),
		AuthRequired:  boolVal(p, "auth_required"),
		AuthDomains:   str(p, "auth_domains"),
		Datasets:      datasets,
		BaseDomain:    baseDomain,
		EnvVars:       envVars,
	}
	return deployer.Deploy(ctx, cfg, cache, nil, logFn)
}

func completeTask(ctx context.Context, baseURL, secret string, taskID uuid.UUID, status store.TaskStatus, extra map[string]interface{}) {
	body := map[string]interface{}{"status": string(status)}
	for k, v := range extra {
		body[k] = v
	}
	b, _ := json.Marshal(body)
	resp, _ := agentPost(fmt.Sprintf("%s/api/agent/tasks/%s/complete", baseURL, taskID),
		secret, "application/json", jsonReader(b))
	if resp != nil {
		resp.Body.Close()
	}
}

func appendLog(ctx context.Context, baseURL, secret string, deploymentID uuid.UUID, line string) {
	body, _ := json.Marshal(map[string]string{"line": line})
	resp, _ := agentPost(fmt.Sprintf("%s/api/deployments/%s/logs", baseURL, deploymentID),
		secret, "application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

// agentGet issues a GET request with the agent secret header.
func agentGet(url, secret string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("X-Agent-Secret", secret)
	}
	return http.DefaultClient.Do(req)
}

// agentPost issues a POST request with the agent secret header.
func agentPost(url, secret, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if secret != "" {
		req.Header.Set("X-Agent-Secret", secret)
	}
	return http.DefaultClient.Do(req)
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

func intVal(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func boolVal(m map[string]interface{}, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func mapStrStr(m map[string]interface{}, key string) map[string]string {
	out := make(map[string]string)
	raw, ok := m[key].(map[string]interface{})
	if !ok {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
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
