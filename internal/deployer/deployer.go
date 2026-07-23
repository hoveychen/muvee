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
	// FixedHostPort, when non-zero, locks the published host port to this value
	// instead of letting Docker pick an ephemeral. The deploy step probes the
	// port first and fails fast if it is already bound.
	FixedHostPort int
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

	// Pre-flight port probe for fixed-port deploys. The old container has been
	// removed already so its own listener no longer counts as a conflict.
	if cfg.FixedHostPort > 0 {
		logFn(fmt.Sprintf("Probing host port %d availability...", cfg.FixedHostPort))
		if err := probePortAvailable(ctx, cfg.FixedHostPort); err != nil {
			return 0, err
		}
	}

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

	// Build docker run command. The host-side spec is "0" by default (Docker
	// picks an ephemeral port, retrieved via `docker port` after startup);
	// when FixedHostPort is set we lock the published port to that value.
	// muvee.* labels let the agent's status reporter group containers and look
	// up the right host-side port mapping after a docker restart reassigns it.
	hostSpec := "0"
	if cfg.FixedHostPort > 0 {
		hostSpec = strconv.Itoa(cfg.FixedHostPort)
	}
	dockerArgs := []string{
		"run", "-d",
		"--name", oldContainer,
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("%s:%d", hostSpec, containerPort),
		"--label", "muvee.domain_prefix=" + cfg.DomainPrefix,
		"--label", "muvee.expose_port=" + strconv.Itoa(containerPort),
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
		// muvee runs as root, so a fresh dir is owned root:root; an image that
		// runs as a non-root user can't write into its bind-mounted workspace.
		// Chown to the image's user on first creation.
		_, statErr := os.Stat(volumeHostPath)
		freshVolume := os.IsNotExist(statErr)
		if err := os.MkdirAll(volumeHostPath, 0755); err != nil {
			return 0, fmt.Errorf("create workspace volume dir: %w", err)
		}
		if freshVolume {
			chownVolumeToImageUser(ctx, volumeHostPath, cfg.ImageTag, logFn)
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

// proxyVarKeys is the canonical set of HTTP proxy environment variable names
// that docker compose v2 forwards from the calling process into containers when
// the user's compose file explicitly inherits them — via value-less environment
// entries (`environment: - HTTP_PROXY`) or YAML interpolation (`${HTTP_PROXY}`).
// Kept in sync with internal/builder/builder.go's collectProxyBuildArgsFrom
// list so both sides of the pipeline are consistent.
var proxyVarKeys = map[string]bool{
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"ALL_PROXY": true, "FTP_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"all_proxy": true, "ftp_proxy": true,
}

// envWithoutProxy returns the process environment with all standard HTTP/HTTPS
// proxy variables stripped. User compose files commonly inherit proxy vars from
// the calling process via value-less environment entries (`environment: - HTTP_PROXY`)
// or YAML variable interpolation (`${HTTP_PROXY}`); stripping them here ensures
// that user containers cannot accidentally route Docker-internal traffic
// (e.g. hub-server, redis) through an external proxy when using those patterns.
//
// Note: `docker run` (single-container path in deployer.go) does NOT need this
// treatment — muvee never passes proxy vars as explicit -e flags to docker run,
// so the single-container path is unaffected. This function is only called on
// the compose subcommand path where the user's compose file controls inheritance.
func envWithoutProxy() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		key := strings.SplitN(e, "=", 2)[0]
		if !proxyVarKeys[key] {
			result = append(result, e)
		}
	}
	return result
}

// envForCompose returns the environment to pass to docker compose subprocesses.
// By default proxy variables are stripped (see envWithoutProxy). When
// DEPLOYER_PROXY_PASSTHROUGH is set to a truthy value (true/1/yes/on), the
// standard proxy variables are re-added from the agent process using an explicit
// whitelist — no other agent-private variables (AGENT_SECRET, DB credentials,
// tokens, etc.) are ever exposed to user containers.
//
// DEPLOYER_PROXY_PASSTHROUGH defaults to false (opposite of
// BUILDER_PROXY_PASSTHROUGH, which defaults to true) because compose deploys
// containers that communicate over Docker-internal networks where an external
// proxy would break intra-service calls. Enable only if user containers
// explicitly need external proxy access and the NO_PROXY list covers all
// internal Docker service names.
func envForCompose() []string {
	base := envWithoutProxy()
	if !deployerProxyPassthrough() {
		return base
	}
	for k := range proxyVarKeys {
		if v := os.Getenv(k); v != "" {
			base = append(base, k+"="+v)
		}
	}
	return base
}

// deployerProxyPassthrough reports whether DEPLOYER_PROXY_PASSTHROUGH is set
// to a truthy value. Symmetric with buildProxyPassthroughFor in builder.go but
// with inverted default: deploy passthrough is off by default.
func deployerProxyPassthrough() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEPLOYER_PROXY_PASSTHROUGH"))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// runCmdCompose runs a docker compose subcommand with proxy variables stripped
// from the subprocess environment (or selectively re-added when
// DEPLOYER_PROXY_PASSTHROUGH is enabled). Use runCmd for non-compose commands.
func runCmdCompose(ctx context.Context, logFn func(string), name string, args ...string) error {
	return runCmdComposeEnv(ctx, logFn, nil, name, args...)
}

// runCmdComposeEnv is like runCmdCompose but appends extraEnv (e.g.
// "DOCKER_CONFIG=/tmp/...") to the compose subprocess environment. Entries in
// extraEnv take precedence over envForCompose() values with the same key.
func runCmdComposeEnv(ctx context.Context, logFn func(string), extraEnv []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(envForCompose(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logFn(string(out))
	}
	return err
}
