package deployer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeConfig is the input for deploying a docker-compose project on a deploy
// node. The project must already have been pinned to this node by the
// scheduler.
type ComposeConfig struct {
	DeploymentID string
	ProjectID    string
	DomainPrefix string
	// GitURL / GitBranch identify the compose source repo. Cloned fresh on the
	// deploy node every deploy.
	GitURL    string
	GitBranch string
	// GitSSHKey / GitUsername / GitToken are optional credentials for private
	// repos. Same shape as the builder flow.
	GitSSHKey   string
	GitUsername string
	GitToken    string
	// ComposeFilePath is the compose file's path relative to the repo root,
	// e.g. "docker-compose.yml".
	ComposeFilePath string
	// ExposeService / ExposePort identify the compose service whose port should
	// be published as the project's host port for Traefik to route to.
	ExposeService string
	ExposePort    int
	// EnvVars are written to a project-level .env file consumed by compose,
	// so they reach all services via standard ${VAR} interpolation.
	EnvVars map[string]string
	// InlineComposeYAML, when non-empty, is written directly into the work
	// directory as docker-compose.yml and the git clone step is skipped. Used
	// by image-only projects whose compose file is synthesised by the
	// scheduler from project.image_ref.
	InlineComposeYAML string
	// VolumeMountPath / VolumeNFSBasePath are only honoured for inline-compose
	// (image-only) deploys. When both are set, the agent injects a bind-mount
	// of {VolumeNFSBasePath}/{ProjectID} into the `app` service via the
	// override file so the workspace API and the running container share the
	// same on-disk directory (mirrors the deployment-type pattern).
	VolumeMountPath   string
	VolumeNFSBasePath string
	// WorkBaseDir is the deploy-node directory under which per-project clones
	// are kept. Defaults to /var/lib/muvee/compose.
	WorkBaseDir string
}

// composeFile captures the subset of a docker-compose file we need to inspect
// (service names, their build directives, and their declared ports).
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	// Build is `omitempty`'d into a raw node so we can detect any presence of
	// the `build:` key (string or mapping form).
	Build *yaml.Node `yaml:"build,omitempty"`
	// Image is required for all services in muvee compose deploys.
	Image string     `yaml:"image,omitempty"`
	Ports []yaml.Node `yaml:"ports,omitempty"`
}

// DeployCompose clones the compose source on the deploy node, validates that
// no `build:` directives are present, writes a small override file that
// publishes the exposed port to a dynamic host port, runs `docker compose
// up -d`, and returns the host port for Traefik routing.
func DeployCompose(ctx context.Context, cfg ComposeConfig, logFn func(string)) (int, error) {
	if cfg.ProjectID == "" {
		return 0, fmt.Errorf("compose deploy: project id is required")
	}
	if cfg.DomainPrefix == "" {
		return 0, fmt.Errorf("compose deploy: domain prefix is required")
	}
	if cfg.GitURL == "" && cfg.InlineComposeYAML == "" {
		return 0, fmt.Errorf("compose deploy: either git_url or inline_compose_yaml is required")
	}
	if cfg.ExposeService == "" || cfg.ExposePort == 0 {
		return 0, fmt.Errorf("compose deploy: expose_service and expose_port are required")
	}
	composeFilePath := cfg.ComposeFilePath
	if composeFilePath == "" {
		composeFilePath = "docker-compose.yml"
	}
	if filepath.IsAbs(composeFilePath) || strings.Contains(composeFilePath, "..") {
		return 0, fmt.Errorf("compose_file_path must be a relative path inside the repo")
	}

	workBase := cfg.WorkBaseDir
	if workBase == "" {
		workBase = "/var/lib/muvee/compose"
	}
	workDir := filepath.Join(workBase, cfg.ProjectID)

	if cfg.InlineComposeYAML != "" {
		// Image-only project: write the synthesised compose file straight to
		// the work dir, skip the git clone entirely.
		if err := os.RemoveAll(workDir); err != nil {
			return 0, fmt.Errorf("clean work dir: %w", err)
		}
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return 0, fmt.Errorf("create work dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(workDir, composeFilePath), []byte(cfg.InlineComposeYAML), 0644); err != nil {
			return 0, fmt.Errorf("write inline compose: %w", err)
		}
		logFn("Using inline compose file (image-only project, no git clone)")
	} else {
		if err := cloneCompose(ctx, cfg, workDir, logFn); err != nil {
			return 0, fmt.Errorf("clone: %w", err)
		}
	}

	composePath := filepath.Join(workDir, composeFilePath)
	composeBytes, err := os.ReadFile(composePath)
	if err != nil {
		return 0, fmt.Errorf("read compose file %s: %w", composeFilePath, err)
	}

	var parsed composeFile
	if err := yaml.Unmarshal(composeBytes, &parsed); err != nil {
		return 0, fmt.Errorf("parse compose file: %w", err)
	}
	if len(parsed.Services) == 0 {
		return 0, fmt.Errorf("compose file declares no services")
	}
	for name, svc := range parsed.Services {
		if svc.Build != nil {
			return 0, fmt.Errorf("service %q has a build: directive — muvee compose projects must use image: only (build externally and reference the resulting image)", name)
		}
		if svc.Image == "" {
			return 0, fmt.Errorf("service %q has no image: — every compose service must declare a pre-built image", name)
		}
	}
	if _, ok := parsed.Services[cfg.ExposeService]; !ok {
		return 0, fmt.Errorf("expose_service %q is not declared in the compose file", cfg.ExposeService)
	}

	// Workspace bind-mount injection (image-only projects):
	// the inline compose file declares no volumes; we add a host bind-mount
	// here so the workspace tab and the container see the same directory.
	// One-shot migration: if a legacy named volume from a previous deploy of
	// this project still has data and the NFS dir is empty, copy it over so
	// data created before the bind-mount switch isn't orphaned.
	var workspaceMount string
	if cfg.InlineComposeYAML != "" && cfg.VolumeMountPath != "" && cfg.VolumeNFSBasePath != "" && cfg.ProjectID != "" {
		nfsHostPath := filepath.Join(cfg.VolumeNFSBasePath, cfg.ProjectID)
		if err := os.MkdirAll(nfsHostPath, 0755); err != nil {
			return 0, fmt.Errorf("create workspace dir: %w", err)
		}
		projectName := composeProjectName(cfg.DomainPrefix)
		legacyVolume := projectName + "_app-data"
		if isEmptyDir(nfsHostPath) && dockerVolumeExists(ctx, legacyVolume) {
			logFn(fmt.Sprintf("Migrating legacy named volume %q into %s...", legacyVolume, nfsHostPath))
			if err := runCmd(ctx, logFn, "docker", "run", "--rm",
				"-v", legacyVolume+":/src:ro",
				"-v", nfsHostPath+":/dst",
				"alpine", "sh", "-c", "cp -a /src/. /dst/",
			); err != nil {
				logFn(fmt.Sprintf("legacy volume migration warning: %v (continuing with empty workspace)", err))
			}
		}
		workspaceMount = fmt.Sprintf("%s:%s:rw", nfsHostPath, cfg.VolumeMountPath)
	}

	// Override file: publish the exposed service's container port to a random
	// host port and stamp muvee.* labels on the expose-service container so the
	// agent can find it again after a restart reassigns the host port. Compose
	// merges this on top of the user's compose file.
	overridePath := filepath.Join(workDir, "muvee.override.yml")
	override := fmt.Sprintf(
		"services:\n"+
			"  %s:\n"+
			"    ports:\n"+
			"      - \"0:%d\"\n"+
			"    labels:\n"+
			"      muvee.domain_prefix: \"%s\"\n"+
			"      muvee.expose_port: \"%d\"\n",
		cfg.ExposeService, cfg.ExposePort, cfg.DomainPrefix, cfg.ExposePort,
	)
	if workspaceMount != "" {
		override += fmt.Sprintf(
			"    volumes:\n"+
				"      - \"%s\"\n",
			workspaceMount,
		)
	}
	if err := os.WriteFile(overridePath, []byte(override), 0644); err != nil {
		return 0, fmt.Errorf("write override file: %w", err)
	}

	// Project-level .env file: env vars sourced from project secrets.
	envPath := filepath.Join(workDir, ".env")
	if len(cfg.EnvVars) > 0 {
		var b strings.Builder
		for k, v := range cfg.EnvVars {
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(v)
			b.WriteString("\n")
		}
		if err := os.WriteFile(envPath, []byte(b.String()), 0600); err != nil {
			return 0, fmt.Errorf("write .env file: %w", err)
		}
	} else {
		// Always create an empty .env so docker compose doesn't warn about it
		// being missing on subsequent runs.
		_ = os.WriteFile(envPath, []byte{}, 0600)
	}

	projectName := composeProjectName(cfg.DomainPrefix)
	composeArgs := func(extra ...string) []string {
		args := []string{
			"compose",
			"-p", projectName,
			"-f", composePath,
			"-f", overridePath,
			"--project-directory", workDir,
		}
		return append(args, extra...)
	}

	logFn(fmt.Sprintf("Pulling compose images for project %s...", projectName))
	if err := runCmd(ctx, logFn, "docker", composeArgs("pull")...); err != nil {
		return 0, fmt.Errorf("docker compose pull: %w", err)
	}

	logFn("Starting compose project (docker compose up -d)...")
	if err := runCmd(ctx, logFn, "docker", composeArgs("up", "-d", "--remove-orphans")...); err != nil {
		return 0, fmt.Errorf("docker compose up: %w", err)
	}

	hostPort, err := composeHostPort(ctx, projectName, composePath, overridePath, workDir, cfg.ExposeService, cfg.ExposePort)
	if err != nil {
		return 0, fmt.Errorf("resolve host port: %w", err)
	}
	logFn(fmt.Sprintf("Compose project up; %s:%d published on host port %d", cfg.ExposeService, cfg.ExposePort, hostPort))
	return hostPort, nil
}

// CleanupCompose tears down a compose project, including its named volumes.
// Used when the project is deleted from muvee.
func CleanupCompose(ctx context.Context, projectID, domainPrefix, composeFilePath, workBaseDir string, logFn func(string)) error {
	workBase := workBaseDir
	if workBase == "" {
		workBase = "/var/lib/muvee/compose"
	}
	workDir := filepath.Join(workBase, projectID)
	if composeFilePath == "" {
		composeFilePath = "docker-compose.yml"
	}
	composePath := filepath.Join(workDir, composeFilePath)
	overridePath := filepath.Join(workDir, "muvee.override.yml")
	projectName := composeProjectName(domainPrefix)

	args := []string{
		"compose", "-p", projectName,
		"-f", composePath, "-f", overridePath,
		"--project-directory", workDir,
		"down", "-v", "--remove-orphans",
	}
	if err := runCmd(ctx, logFn, "docker", args...); err != nil {
		// down failures are not fatal — the project may already be gone.
		logFn(fmt.Sprintf("compose down warning: %v", err))
	}
	if err := os.RemoveAll(workDir); err != nil {
		return fmt.Errorf("remove work dir: %w", err)
	}
	return nil
}

// StopCompose tears down a compose project but keeps its named volumes.
// Used when migrating to a different node (rare for compose since they are
// pinned, but still useful for a manual stop).
func StopCompose(ctx context.Context, projectID, domainPrefix, composeFilePath, workBaseDir string, logFn func(string)) error {
	workBase := workBaseDir
	if workBase == "" {
		workBase = "/var/lib/muvee/compose"
	}
	workDir := filepath.Join(workBase, projectID)
	if composeFilePath == "" {
		composeFilePath = "docker-compose.yml"
	}
	composePath := filepath.Join(workDir, composeFilePath)
	overridePath := filepath.Join(workDir, "muvee.override.yml")
	projectName := composeProjectName(domainPrefix)

	args := []string{
		"compose", "-p", projectName,
		"-f", composePath, "-f", overridePath,
		"--project-directory", workDir,
		"down", "--remove-orphans",
	}
	return runCmd(ctx, logFn, "docker", args...)
}

// composeProjectName mirrors the convention used for single-container deploys
// (`muvee-<prefix>`) so operators can recognize muvee-managed projects in
// `docker compose ls`.
func composeProjectName(domainPrefix string) string {
	return "muvee-" + domainPrefix
}

func composeHostPort(ctx context.Context, projectName, composePath, overridePath, workDir, service string, containerPort int) (int, error) {
	args := []string{
		"compose", "-p", projectName,
		"-f", composePath, "-f", overridePath,
		"--project-directory", workDir,
		"port", service, strconv.Itoa(containerPort),
	}
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("docker compose port: %w", err)
	}
	return parseHostPort(strings.TrimSpace(string(out)))
}

// cloneCompose fetches the compose source onto the deploy node. If the work
// dir already exists, it's removed first so the deploy starts from a clean
// checkout (named volumes survive because they live in docker, not in the
// work dir).
func cloneCompose(ctx context.Context, cfg ComposeConfig, workDir string, logFn func(string)) error {
	if err := os.RemoveAll(workDir); err != nil {
		return fmt.Errorf("clean work dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(workDir), 0755); err != nil {
		return fmt.Errorf("create work base: %w", err)
	}

	gitURL := cfg.GitURL
	gitEnv := os.Environ()
	cleanup := func() {}

	if cfg.GitSSHKey != "" {
		// Write SSH key to a temp file and force git to use it via GIT_SSH_COMMAND.
		keyFile, err := os.CreateTemp("", "muvee-compose-key-*")
		if err != nil {
			return fmt.Errorf("create ssh key tempfile: %w", err)
		}
		if err := os.Chmod(keyFile.Name(), 0600); err != nil {
			keyFile.Close()
			os.Remove(keyFile.Name())
			return fmt.Errorf("chmod ssh key: %w", err)
		}
		if _, err := keyFile.WriteString(cfg.GitSSHKey); err != nil {
			keyFile.Close()
			os.Remove(keyFile.Name())
			return fmt.Errorf("write ssh key: %w", err)
		}
		keyFile.Close()
		gitEnv = append(gitEnv,
			fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", keyFile.Name()))
		cleanup = func() { os.Remove(keyFile.Name()) }
	} else if cfg.GitUsername != "" && cfg.GitToken != "" && strings.HasPrefix(gitURL, "https://") {
		// Inline credentials into the URL: https://user:token@host/...
		gitURL = "https://" + cfg.GitUsername + ":" + cfg.GitToken + "@" + strings.TrimPrefix(gitURL, "https://")
	}
	defer cleanup()

	branch := cfg.GitBranch
	if branch == "" {
		branch = "main"
	}

	logFn(fmt.Sprintf("Cloning %s (branch %s) into %s...", redactGitURL(gitURL), branch, workDir))
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", "--branch", branch, gitURL, workDir)
	cmd.Env = gitEnv
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logFn(string(out))
	}
	if err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

// isEmptyDir reports whether path is an empty directory. Returns false on any
// stat/read error so we never trigger migration into a directory we can't read.
func isEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// dockerVolumeExists reports whether a docker named volume with the given name
// is present on this host. Used by the one-shot legacy-volume migration for
// image-only projects switching from named volumes to NFS bind-mounts.
func dockerVolumeExists(ctx context.Context, name string) bool {
	if name == "" {
		return false
	}
	if err := exec.CommandContext(ctx, "docker", "volume", "inspect", name).Run(); err != nil {
		return false
	}
	return true
}

// redactGitURL strips inline HTTPS credentials so they don't end up in logs.
func redactGitURL(url string) string {
	if !strings.HasPrefix(url, "https://") {
		return url
	}
	rest := strings.TrimPrefix(url, "https://")
	at := strings.Index(rest, "@")
	if at < 0 {
		return url
	}
	return "https://***@" + rest[at+1:]
}
