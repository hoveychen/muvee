package deployer

import (
	"context"
	"fmt"
	"os/exec"
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
	for _, ds := range cfg.Datasets {
		dsID, _ := uuid.Parse(ds.ID)
		mounts = append(mounts, datacache.DatasetMount{
			Dataset: &store.Dataset{
				ID:        dsID,
				Name:      ds.Name,
				NFSPath:   ds.NFSPath,
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

	for _, m := range allMounts {
		dockerArgs = append(dockerArgs, "-v", m)
	}

	dockerArgs = append(dockerArgs, cfg.ImageTag)
	logFn(fmt.Sprintf("Starting container: docker %s", strings.Join(dockerArgs, " ")))
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
