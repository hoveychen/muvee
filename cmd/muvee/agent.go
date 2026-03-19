package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/builder"
	"github.com/hoveychen/muvee/internal/datacache"
	"github.com/hoveychen/muvee/internal/deployer"
	"github.com/hoveychen/muvee/internal/store"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

func runAgent() {
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
	// NODE_NAME lets operators pin a stable identity to the agent so that
	// container restarts / image upgrades do not register a new node.
	// Defaults to the OS hostname when not set.
	if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
		hostname = nodeName
	}

	hostIP := os.Getenv("HOST_IP")
	if hostIP == "" {
		// When running inside Docker (standalone mode) the outbound IP is the container's
		// Docker network address, but deployed-app containers bind ports to the Docker host
		// via "-p 0:<port>". Traefik (also inside Docker) must reach those ports through the
		// Docker host's gateway IP on the shared network, not the agent container's IP.
		// Outside Docker (multi-node native agents) the outbound IP is the machine's own IP,
		// which is exactly what Traefik needs.
		if isRunningInDocker() {
			hostIP = detectGatewayIP()
		}
		if hostIP == "" {
			hostIP = detectOutboundIP(controlPlaneURL)
		}
	}
	if hostIP == "" {
		log.Println("Warning: could not detect HOST_IP; deploy routes may not be reachable by Traefik")
	} else {
		log.Printf("Using HOST_IP=%s", hostIP)
	}

	maxStorage := int64(100 * 1024 * 1024 * 1024) // 100 GB default
	nodeID := registerNode(ctx, controlPlaneURL, agentSecret, hostname, nodeRole, hostIP, maxStorage)
	log.Printf("Agent registered as node %s (role=%s, ip=%s)", nodeID, nodeRole, hostIP)

	agentCfg := fetchAgentConfig(ctx, controlPlaneURL, agentSecret)
	registryAddr := agentCfg["registry_addr"]
	baseDomain := agentCfg["base_domain"]
	volumeNFSBasePath := agentCfg["volume_nfs_base_path"]
	datasetNFSBasePath := agentCfg["dataset_nfs_base_path"]

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
		go runContainerStatusReporter(ctx, controlPlaneURL, agentSecret)
	}
	go runNodeMetricsReporter(ctx, controlPlaneURL, agentSecret, nodeID, baseDir)
	go runAgentHealthReporter(ctx, controlPlaneURL, agentSecret, nodeID, nodeRole, registryAddr, volumeNFSBasePath, datasetNFSBasePath)

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
				go handleTask(ctx, task, controlPlaneURL, agentSecret, nodeID, nodeRole, registryAddr, baseDir, baseDomain, volumeNFSBasePath, datasetNFSBasePath, cache)
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

// isRunningInDocker reports whether the current process is running inside a Docker container
// by checking for the presence of /.dockerenv (created by Docker on container start).
func isRunningInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// detectGatewayIP returns the default-route gateway IP, which is the Docker host's IP
// on the container's network. In standalone Docker deployments this is the address
// Traefik can use to reach ports published with "-p host:container" on the host.
func detectGatewayIP() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	// Output looks like: "default via 172.20.0.1 dev eth0 ..."
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Fields(line)
		for i, p := range parts {
			if p == "via" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	return ""
}

// extractHost parses a URL and returns "host" (without port) suitable for net.Dial.
func extractHost(rawURL string) string {
	// Strip scheme
	s := rawURL
	if i := len("https://"); len(s) > i && s[:i] == "https://" {
		s = s[i:]
	} else if i := len("http://"); len(s) > i && s[:i] == "http://" {
		s = s[i:]
	}
	// Strip path
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	// Strip port — net.Dial needs host:port; we'll append ":80" ourselves
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	return s
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

func dockerLogin(registry, user, password string) error {
	cmd := exec.Command("docker", "login", registry, "-u", user, "--password-stdin")
	cmd.Stdin = strings.NewReader(password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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

func handleTask(ctx context.Context, task *store.Task, baseURL, secret string, nodeID uuid.UUID, role, registryAddr, baseDir, baseDomain, volumeNFSBasePath, datasetNFSBasePath string, cache *datacache.Cache) {
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
		hostPort, err := runDeploy(ctx, task, cache, baseDomain, volumeNFSBasePath, datasetNFSBasePath, func(line string) {
			appendLog(ctx, baseURL, secret, task.DeploymentID, line)
		})
		taskErr = err
		if err == nil && hostPort > 0 {
			extra["host_port"] = hostPort
		}

	case store.TaskTypeCleanup:
		taskErr = runCleanup(ctx, task)
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
	}
	imageTag, err := builder.Build(ctx, cfg, logFn)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"image_tag": imageTag}, nil
}

func runDeploy(ctx context.Context, task *store.Task, cache *datacache.Cache, baseDomain, volumeNFSBasePath, datasetNFSBasePath string, logFn func(string)) (int, error) {
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
		DeploymentID:       str(p, "deployment_id"),
		ProjectID:          str(p, "project_id"),
		DomainPrefix:       str(p, "domain_prefix"),
		ImageTag:           str(p, "image_tag"),
		ContainerPort:      intVal(p, "container_port"),
		AuthRequired:       boolVal(p, "auth_required"),
		AuthDomains:        str(p, "auth_domains"),
		MemoryLimit:        str(p, "memory_limit"),
		VolumeMountPath:    str(p, "volume_mount_path"),
		VolumeNFSBasePath:  volumeNFSBasePath,
		DatasetNFSBasePath: datasetNFSBasePath,
		Datasets:           datasets,
		BaseDomain:         baseDomain,
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

func runCleanup(ctx context.Context, task *store.Task) error {
	domainPrefix := str(task.Payload, "domain_prefix")
	if domainPrefix == "" {
		return fmt.Errorf("cleanup task missing domain_prefix")
	}
	containerName := "muvee-" + domainPrefix
	log.Printf("Cleanup: removing stale container %s", containerName)
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerName)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("Cleanup docker rm output: %s", strings.TrimSpace(string(out)))
	}
	// Ignore "no such container" errors – the container may already be gone.
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker rm -f %s: %w", containerName, err)
	}
	return nil
}

// runContainerStatusReporter periodically inspects all muvee-* containers on this node
// and reports their restart counts, OOM-killed flags, and resource metrics to the control plane.
func runContainerStatusReporter(ctx context.Context, baseURL, secret string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reportContainerStatuses(ctx, baseURL, secret)
			reportContainerMetrics(ctx, baseURL, secret)
		}
	}
}

func reportContainerStatuses(ctx context.Context, baseURL, secret string) {
	out, err := exec.CommandContext(ctx, "docker", "ps",
		"--filter", "name=muvee-",
		"--format", "{{.Names}}").Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return
	}

	type containerStatus struct {
		DomainPrefix string `json:"domain_prefix"`
		RestartCount int    `json:"restart_count"`
		OOMKilled    bool   `json:"oom_killed"`
	}
	var statuses []containerStatus

	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "muvee-") {
			continue
		}
		domainPrefix := strings.TrimPrefix(name, "muvee-")

		inspectOut, err := exec.CommandContext(ctx, "docker", "inspect",
			"--format", `{"restart_count":{{.RestartCount}},"oom_killed":{{.State.OOMKilled}}}`,
			name).Output()
		if err != nil {
			continue
		}
		var st struct {
			RestartCount int  `json:"restart_count"`
			OOMKilled    bool `json:"oom_killed"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(inspectOut), &st); err != nil {
			log.Printf("Failed to parse inspect output for %s: %v", name, err)
			continue
		}
		statuses = append(statuses, containerStatus{
			DomainPrefix: domainPrefix,
			RestartCount: st.RestartCount,
			OOMKilled:    st.OOMKilled,
		})
	}

	if len(statuses) == 0 {
		return
	}
	body, _ := json.Marshal(statuses)
	resp, _ := agentPost(baseURL+"/api/agent/container-statuses", secret, "application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

// ─── Container Metrics Reporter ───────────────────────────────────────────────

type containerMetricReport struct {
	DomainPrefix    string  `json:"domain_prefix"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemUsageBytes   int64   `json:"mem_usage_bytes"`
	MemLimitBytes   int64   `json:"mem_limit_bytes"`
	NetRxBytes      int64   `json:"net_rx_bytes"`
	NetTxBytes      int64   `json:"net_tx_bytes"`
	BlockReadBytes  int64   `json:"block_read_bytes"`
	BlockWriteBytes int64   `json:"block_write_bytes"`
}

// reportContainerMetrics collects resource stats for all muvee-* containers via
// `docker stats --no-stream` and ships them to the control plane.
func reportContainerMetrics(ctx context.Context, baseURL, secret string) {
	// 1. Enumerate running muvee containers.
	psOut, err := exec.CommandContext(ctx, "docker", "ps",
		"--filter", "name=muvee-",
		"--format", "{{.Names}}").Output()
	if err != nil || len(bytes.TrimSpace(psOut)) == 0 {
		return
	}
	var muveeNames []string
	for _, name := range strings.Split(strings.TrimSpace(string(psOut)), "\n") {
		name = strings.TrimSpace(name)
		if strings.HasPrefix(name, "muvee-") {
			muveeNames = append(muveeNames, name)
		}
	}
	if len(muveeNames) == 0 {
		return
	}

	// 2. Collect stats (one-shot, no streaming).
	// docker stats --no-stream outputs one JSON object per line when using --format.
	statsArgs := []string{
		"stats", "--no-stream",
		"--format",
		`{"name":"{{.Name}}","cpu":"{{.CPUPerc}}","mem":"{{.MemUsage}}","net":"{{.NetIO}}","block":"{{.BlockIO}}"}`,
	}
	statsArgs = append(statsArgs, muveeNames...)
	statsOut, err := exec.CommandContext(ctx, "docker", statsArgs...).Output()
	if err != nil || len(bytes.TrimSpace(statsOut)) == 0 {
		return
	}

	var reports []containerMetricReport
	for _, line := range strings.Split(strings.TrimSpace(string(statsOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw struct {
			Name  string `json:"name"`
			CPU   string `json:"cpu"`
			Mem   string `json:"mem"`
			Net   string `json:"net"`
			Block string `json:"block"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			log.Printf("docker stats parse error: %v (line: %s)", err, line)
			continue
		}
		if !strings.HasPrefix(raw.Name, "muvee-") {
			continue
		}
		domainPrefix := strings.TrimPrefix(raw.Name, "muvee-")
		memUsage, memLimit := parseDockerIOPair(raw.Mem)
		netRx, netTx := parseDockerIOPair(raw.Net)
		blockRead, blockWrite := parseDockerIOPair(raw.Block)
		reports = append(reports, containerMetricReport{
			DomainPrefix:    domainPrefix,
			CPUPercent:      parseDockerPercent(raw.CPU),
			MemUsageBytes:   memUsage,
			MemLimitBytes:   memLimit,
			NetRxBytes:      netRx,
			NetTxBytes:      netTx,
			BlockReadBytes:  blockRead,
			BlockWriteBytes: blockWrite,
		})
	}

	if len(reports) == 0 {
		return
	}
	body, _ := json.Marshal(reports)
	resp, _ := agentPost(baseURL+"/api/agent/container-metrics", secret, "application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

// parseDockerPercent converts Docker CPU percentage string (e.g. "2.34%") to float64.
func parseDockerPercent(s string) float64 {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseDockerIOPair splits "A / B" and returns the two parsed byte counts.
// Docker uses both binary units (KiB, MiB, GiB) and SI units (kB, MB, GB).
func parseDockerIOPair(s string) (int64, int64) {
	parts := strings.SplitN(s, " / ", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseDockerBytes(parts[0]), parseDockerBytes(parts[1])
}

// parseDockerBytes converts a Docker size string like "1.5GiB", "512MB", "100kB" to bytes.
func parseDockerBytes(s string) int64 {
	s = strings.TrimSpace(s)
	type unit struct {
		suffix string
		mult   float64
	}
	units := []unit{
		{"TiB", 1 << 40},
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
		{"TB", 1e12},
		{"GB", 1e9},
		{"MB", 1e6},
		{"kB", 1e3},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			val, _ := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			return int64(val * u.mult)
		}
	}
	val, _ := strconv.ParseFloat(s, 64)
	return int64(val)
}

// ─── Node Metrics Reporter ────────────────────────────────────────────────────

type nodeMetricReport struct {
	NodeID         string  `json:"node_id"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemTotalBytes  int64   `json:"mem_total_bytes"`
	MemUsedBytes   int64   `json:"mem_used_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
}

// runNodeMetricsReporter periodically collects host-level resource metrics and
// ships them to the control plane.
func runNodeMetricsReporter(ctx context.Context, baseURL, secret string, nodeID uuid.UUID, dataDir string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reportNodeMetrics(ctx, baseURL, secret, nodeID, dataDir)
		}
	}
}

func reportNodeMetrics(ctx context.Context, baseURL, secret string, nodeID uuid.UUID, dataDir string) {
	cpuPct := collectCPUPercent()
	memTotal, memUsed := collectMemory()
	diskTotal, diskUsed := collectDisk(dataDir)
	load1, load5, load15 := collectLoadAvg()

	report := nodeMetricReport{
		NodeID:         nodeID.String(),
		CPUPercent:     cpuPct,
		MemTotalBytes:  memTotal,
		MemUsedBytes:   memUsed,
		DiskTotalBytes: diskTotal,
		DiskUsedBytes:  diskUsed,
		Load1:          load1,
		Load5:          load5,
		Load15:         load15,
	}
	body, _ := json.Marshal(report)
	resp, _ := agentPost(baseURL+"/api/agent/node-metrics", secret, "application/json", jsonReader(body))
	if resp != nil {
		resp.Body.Close()
	}
}

// collectCPUPercent returns overall CPU usage percentage averaged over 500 ms.
// Uses gopsutil for cross-platform support (Linux, macOS, Windows).
func collectCPUPercent() float64 {
	pcts, err := cpu.Percent(500*time.Millisecond, false)
	if err != nil || len(pcts) == 0 {
		return 0
	}
	return pcts[0]
}

// collectMemory returns total and used physical memory in bytes.
func collectMemory() (total, used int64) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, 0
	}
	return int64(v.Total), int64(v.Used)
}

// collectDisk returns disk total/used bytes for the filesystem containing dataDir.
func collectDisk(dataDir string) (total, used int64) {
	if dataDir == "" {
		dataDir = "/"
	}
	usage, err := disk.Usage(dataDir)
	if err != nil {
		usage, err = disk.Usage("/")
		if err != nil {
			return 0, 0
		}
	}
	return int64(usage.Total), int64(usage.Used)
}

// collectLoadAvg returns 1/5/15-minute load averages.
// On Windows, gopsutil returns 0 for all values (not supported by the OS).
func collectLoadAvg() (load1, load5, load15 float64) {
	avg, err := load.Avg()
	if err != nil {
		return 0, 0, 0
	}
	return avg.Load1, avg.Load5, avg.Load15
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

func jsonReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// ─── Agent Health Reporter ────────────────────────────────────────────────────

type agentHealthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`  // "ok" | "warning" | "error"
	Message string `json:"message"`
}

// runAgentHealthReporter runs health checks on the agent and reports them to the control plane.
func runAgentHealthReporter(ctx context.Context, baseURL, secret string, nodeID uuid.UUID, role, registryAddr, volumeNFSBasePath, datasetNFSBasePath string) {
	// Initial check shortly after startup.
	time.Sleep(10 * time.Second)
	reportAgentHealth(ctx, baseURL, secret, nodeID, role, registryAddr, volumeNFSBasePath, datasetNFSBasePath)

	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reportAgentHealth(ctx, baseURL, secret, nodeID, role, registryAddr, volumeNFSBasePath, datasetNFSBasePath)
		}
	}
}

func reportAgentHealth(ctx context.Context, baseURL, secret string, nodeID uuid.UUID, role, registryAddr, volumeNFSBasePath, datasetNFSBasePath string) {
	checks := collectAgentHealthChecks(role, registryAddr, volumeNFSBasePath, datasetNFSBasePath)
	body, _ := json.Marshal(checks)
	resp, _ := agentPost(
		fmt.Sprintf("%s/api/agent/health-report?node_id=%s", baseURL, nodeID),
		secret, "application/json", jsonReader(body),
	)
	if resp != nil {
		resp.Body.Close()
	}
}

func collectAgentHealthChecks(role, registryAddr, volumeNFSBasePath, datasetNFSBasePath string) []agentHealthCheck {
	var checks []agentHealthCheck

	// 1. Docker daemon
	out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Output()
	if err != nil {
		checks = append(checks, agentHealthCheck{
			Name: "docker", Status: "error",
			Message: fmt.Sprintf("docker daemon not reachable: %v", err),
		})
	} else {
		checks = append(checks, agentHealthCheck{
			Name: "docker", Status: "ok",
			Message: fmt.Sprintf("docker %s", strings.TrimSpace(string(out))),
		})
	}

	// 2. Registry connectivity
	if registryAddr != "" {
		scheme := "https"
		addr := registryAddr
		if strings.HasPrefix(addr, "http://") {
			scheme = "http"
			addr = strings.TrimPrefix(addr, "http://")
		} else {
			addr = strings.TrimPrefix(addr, "https://")
		}
		registryURL := scheme + "://" + addr + "/v2/"
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(registryURL)
		if err != nil {
			checks = append(checks, agentHealthCheck{
				Name: "registry", Status: "error",
				Message: fmt.Sprintf("cannot reach registry %s: %v", registryURL, err),
			})
		} else {
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 401 {
				checks = append(checks, agentHealthCheck{
					Name: "registry", Status: "ok",
					Message: fmt.Sprintf("registry reachable (HTTP %d)", resp.StatusCode),
				})
			} else {
				checks = append(checks, agentHealthCheck{
					Name: "registry", Status: "warning",
					Message: fmt.Sprintf("registry returned HTTP %d", resp.StatusCode),
				})
			}
		}
	} else {
		checks = append(checks, agentHealthCheck{
			Name: "registry", Status: "warning",
			Message: "REGISTRY_ADDR not configured",
		})
	}

	// 3. NFS volume path (relevant for both roles)
	checks = append(checks, agentCheckPath("nfs_volume", volumeNFSBasePath))

	// 4. NFS dataset path
	checks = append(checks, agentCheckPath("nfs_dataset", datasetNFSBasePath))

	// 5. Docker build support (builder only)
	if role == "builder" {
		out, err := exec.Command("docker", "buildx", "version").Output()
		if err != nil {
			checks = append(checks, agentHealthCheck{
				Name: "docker_buildx", Status: "warning",
				Message: "docker buildx not available; builds may fail",
			})
		} else {
			checks = append(checks, agentHealthCheck{
				Name: "docker_buildx", Status: "ok",
				Message: strings.TrimSpace(string(out)),
			})
		}
	}

	return checks
}

func agentCheckPath(name, path string) agentHealthCheck {
	if path == "" {
		return agentHealthCheck{Name: name, Status: "warning", Message: "path not configured"}
	}
	if _, err := os.Stat(path); err != nil {
		return agentHealthCheck{Name: name, Status: "error", Message: fmt.Sprintf("cannot access %s: %v", path, err)}
	}
	return agentHealthCheck{Name: name, Status: "ok", Message: fmt.Sprintf("%s is accessible", path)}
}
