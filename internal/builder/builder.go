package builder

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type BuildConfig struct {
	GitURL         string
	GitBranch      string
	DockerfilePath string
	DeploymentID   string
	ProjectID      string
	RegistryAddr   string
	// SSHKey is the PEM-encoded private key used for git clone over SSH.
	// If empty, git clone uses the default SSH agent / HTTPS.
	SSHKey string
	// GitUsername and GitToken are used for HTTPS authentication.
	// The builder rewrites the git URL to https://GitUsername:GitToken@host/...
	// For GitHub fine-grained PATs, set GitUsername to "x-access-token".
	GitUsername string
	GitToken    string
	// BuildSecrets are passed to docker buildx via --secret id=<key>,src=<tempfile>.
	// Inside Dockerfile they are available at /run/secrets/<key>.
	BuildSecrets map[string]string
}

func Build(ctx context.Context, cfg BuildConfig, logFn func(string)) (string, error) {
	workDir, err := os.MkdirTemp("", "muvee-build-"+cfg.ProjectID+"-*")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(workDir)

	cloneURL := cfg.GitURL

	// Write SSH key to a temp file if provided, and configure GIT_SSH_COMMAND.
	var gitEnv []string
	if cfg.SSHKey != "" {
		keyFile, err := os.CreateTemp("", "muvee-sshkey-*")
		if err != nil {
			return "", fmt.Errorf("create ssh key file: %w", err)
		}
		defer os.Remove(keyFile.Name())
		if err := os.WriteFile(keyFile.Name(), []byte(cfg.SSHKey), 0600); err != nil {
			return "", fmt.Errorf("write ssh key file: %w", err)
		}
		gitEnv = append(gitEnv, fmt.Sprintf(
			"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
			keyFile.Name(),
		))
	}

	// Inject HTTPS credentials into the URL if provided.
	// This supports GitHub fine-grained PATs (username=x-access-token) and
	// other HTTPS-based git authentication (GitLab PATs, etc.).
	if cfg.GitUsername != "" && cfg.GitToken != "" {
		u, err := url.Parse(cfg.GitURL)
		if err != nil {
			return "", fmt.Errorf("parse git url: %w", err)
		}
		u.User = url.UserPassword(cfg.GitUsername, cfg.GitToken)
		cloneURL = u.String()
	}

	// Clone — log the original URL (without credentials) for safety.
	logFn(fmt.Sprintf("Cloning %s@%s...", cfg.GitURL, cfg.GitBranch))
	cloneArgs := []string{"clone", "--depth=1", "--branch", cfg.GitBranch, cloneURL, workDir}
	if err := runCmdEnv(ctx, logFn, gitEnv, "git", cloneArgs...); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}

	// Resolve commit SHA
	sha, err := gitRevParse(ctx, workDir, "HEAD")
	if err != nil {
		sha = fmt.Sprintf("%d", time.Now().Unix())
	}
	sha = sha[:min(12, len(sha))]
	logFn(fmt.Sprintf("Commit: %s", sha))

	imageTag := fmt.Sprintf("%s/%s:%s", cfg.RegistryAddr, cfg.ProjectID, sha)
	logFn(fmt.Sprintf("Building image %s...", imageTag))

	dockerfilePath := cfg.DockerfilePath
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(workDir, dockerfilePath)
	}

	buildArgs := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-f", dockerfilePath,
		"-t", imageTag,
		"--push",
	}
	var secretFiles []string
	for id, value := range cfg.BuildSecrets {
		if id == "" {
			continue
		}
		secretFile, err := os.CreateTemp("", "muvee-build-secret-*")
		if err != nil {
			return "", fmt.Errorf("create build secret file: %w", err)
		}
		secretFiles = append(secretFiles, secretFile.Name())
		if err := os.WriteFile(secretFile.Name(), []byte(value), 0600); err != nil {
			return "", fmt.Errorf("write build secret file: %w", err)
		}
		buildArgs = append(buildArgs, "--secret", fmt.Sprintf("id=%s,src=%s", id, secretFile.Name()))
	}
	defer func() {
		for _, f := range secretFiles {
			_ = os.Remove(f)
		}
	}()
	buildArgs = append(buildArgs, workDir)
	if err := runCmd(ctx, logFn, "docker", buildArgs...); err != nil {
		return "", fmt.Errorf("docker build: %w", err)
	}
	logFn(fmt.Sprintf("Successfully pushed %s", imageTag))
	return imageTag, nil
}

func runCmd(ctx context.Context, logFn func(string), name string, args ...string) error {
	return runCmdEnv(ctx, logFn, nil, name, args...)
}

func runCmdEnv(ctx context.Context, logFn func(string), env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	go readLines(stdout, logFn)
	go readLines(stderr, logFn)
	return cmd.Wait()
}

func readLines(r interface{ Read([]byte) (int, error) }, logFn func(string)) {
	buf := make([]byte, 4096)
	var leftover string
	for {
		n, err := r.Read(buf)
		if n > 0 {
			combined := leftover + string(buf[:n])
			lines := strings.Split(combined, "\n")
			for _, line := range lines[:len(lines)-1] {
				if line != "" {
					logFn(line)
				}
			}
			leftover = lines[len(lines)-1]
		}
		if err != nil {
			if leftover != "" {
				logFn(leftover)
			}
			return
		}
	}
}

func gitRevParse(ctx context.Context, dir, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", ref)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
