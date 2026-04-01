package deployer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/datacache"
	"github.com/hoveychen/muvee/internal/store"
)

type Config struct {
	DeploymentID  string
	ProjectID     string
	DomainPrefix  string
	ImageTag      string
	ContainerPort int // port the container listens on (default 8080)
	AuthRequired  bool
	AuthDomains   string
	Datasets      []DatasetSpec
	BaseDomain    string
	RegistryAddr  string
	// EnvVars are injected into the container as environment variables.
	EnvVars map[string]string
	// MemoryLimit sets the Docker --memory flag (e.g. "4g", "512m").
	// Empty string means no limit.
	MemoryLimit string
	// VolumeNFSBasePath is the base NFS directory on the deploy node (e.g. /mnt/nfs/volumes).
	// If set together with VolumeMountPath, a per-project subdirectory is bind-mounted.
	VolumeNFSBasePath string
	// DatasetNFSBasePath is the base NFS directory for dataset files (e.g. /mnt/nfs/datasets).
	// DatasetSpec.NFSPath is treated as a sub-path under this base.
	DatasetNFSBasePath string
	// VolumeMountPath is the container-internal path where the workspace volume is mounted.
	VolumeMountPath string
}

type DatasetSpec struct {
	ID        string
	Name      string
	NFSPath   string
	Version   int64
	SizeBytes int64
	MountMode string
}

// Deploy starts the container on the local Docker daemon and returns the host port
// that was dynamically assigned. Traefik discovers this endpoint via the HTTP provider
// served by the muvee control plane.
func Deploy(ctx context.Context, cfg Config, cache *datacache.Cache, st *store.Store, logFn func(string)) (int, error) {
	deploymentID, err := uuid.Parse(cfg.DeploymentID)
	if err != nil {
		return 0, fmt.Errorf("invalid deployment id: %w", err)
	}

	containerPort := cfg.ContainerPort
	if containerPort == 0 {
		containerPort = 8080
	}

	// Stop old container for this project (rolling update)
	oldContainer := "muvee-" + cfg.DomainPrefix
	logFn(fmt.Sprintf("Stopping old container %s (if any)...", oldContainer))
	_ = runCmd(ctx, logFn, "docker", "rm", "-f", oldContainer)

	// Prepare dataset mounts
	var mounts []datacache.DatasetMount
	needsDatasetBase := false
	for _, ds := range cfg.Datasets {
		if !filepath.IsAbs(ds.NFSPath) {
			needsDatasetBase = true
			break
		}
	}
	if needsDatasetBase && cfg.DatasetNFSBasePath == "" {
		return 0, fmt.Errorf("dataset NFS base path is not configured")
	}
	for _, ds := range cfg.Datasets {
		dsID, _ := uuid.Parse(ds.ID)
		datasetPath := ds.NFSPath
		if !filepath.IsAbs(ds.NFSPath) {
			datasetPath = filepath.Join(cfg.DatasetNFSBasePath, ds.NFSPath)
		}
		mounts = append(mounts, datacache.DatasetMount{
			Dataset: &store.Dataset{
				ID:        dsID,
				Name:      ds.Name,
				NFSPath:   datasetPath,
				Version:   ds.Version,
				SizeBytes: ds.SizeBytes,
			},
			MountMode: store.MountMode(ds.MountMode),
		})
	}

	logFn("Preparing dataset mounts...")
	depMounts, rwMounts, err := cache.SetupMounts(ctx, deploymentID, mounts)
	if err != nil {
		return 0, fmt.Errorf("setup mounts: %w", err)
	}
	allMounts := append(depMounts, rwMounts...)

	// Build docker run command. Port 0 on the host lets Docker pick a free port.
	// The actual assigned port is retrieved via `docker port` after startup.
	dockerArgs := []string{
		"run", "-d",
		"--name", oldContainer,
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("0:%d", containerPort),
	}
	if cfg.MemoryLimit != "" {
		dockerArgs = append(dockerArgs, "--memory", cfg.MemoryLimit)
		dockerArgs = append(dockerArgs, "--memory-swap", cfg.MemoryLimit) // disable swap
	}

	for _, m := range allMounts {
		dockerArgs = append(dockerArgs, "-v", m)
	}

	// Workspace volume: bind-mount a per-project NFS directory into the container.
	if cfg.VolumeNFSBasePath != "" && cfg.VolumeMountPath != "" {
		volumeHostPath := filepath.Join(cfg.VolumeNFSBasePath, cfg.ProjectID)
		logFn(fmt.Sprintf("Creating workspace volume directory: %s", volumeHostPath))
		if err := os.MkdirAll(volumeHostPath, 0755); err != nil {
			return 0, fmt.Errorf("create workspace volume dir: %w", err)
		}
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:%s:rw", volumeHostPath, cfg.VolumeMountPath))
	}

	for k, v := range cfg.EnvVars {
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}

	dockerArgs = append(dockerArgs, cfg.ImageTag)
	// Log the command with secret values redacted.
	redactedArgs := make([]string, len(dockerArgs))
	copy(redactedArgs, dockerArgs)
	for i, arg := range redactedArgs {
		if i > 0 && redactedArgs[i-1] == "-e" {
			if eqIdx := strings.Index(arg, "="); eqIdx >= 0 {
				redactedArgs[i] = arg[:eqIdx+1] + "***"
			}
		}
	}
	logFn(fmt.Sprintf("Starting container: docker %s", strings.Join(redactedArgs, " ")))
	if err := runCmd(ctx, logFn, "docker", dockerArgs...); err != nil {
		return 0, fmt.Errorf("docker run: %w", err)
	}

	// Retrieve the dynamically assigned host port.
	out, err := exec.CommandContext(ctx, "docker", "port", oldContainer, strconv.Itoa(containerPort)).Output()
	if err != nil {
		return 0, fmt.Errorf("docker port lookup: %w", err)
	}
	hostPort, err := parseHostPort(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse host port from %q: %w", strings.TrimSpace(string(out)), err)
	}

	logFn(fmt.Sprintf("Container started, listening on host port %d.", hostPort))
	return hostPort, nil
}

// parseHostPort extracts the port number from `docker port` output.
// Examples: "0.0.0.0:32768", "[::]:32768", "0.0.0.0:32768\n[::]:32768"
func parseHostPort(raw string) (int, error) {
	// Take first line only
	line := strings.SplitN(raw, "\n", 2)[0]
	// Find last ":" to handle IPv6 addresses
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return 0, fmt.Errorf("unexpected format: %q", line)
	}
	return strconv.Atoi(line[idx+1:])
}

func runCmd(ctx context.Context, logFn func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logFn(string(out))
	}
	return err
}
