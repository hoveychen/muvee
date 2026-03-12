package builder

import (
	"context"
	"fmt"
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
}

func Build(ctx context.Context, cfg BuildConfig, logFn func(string)) (string, error) {
	workDir, err := os.MkdirTemp("", "muvee-build-"+cfg.ProjectID+"-*")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Clone
	logFn(fmt.Sprintf("Cloning %s@%s...", cfg.GitURL, cfg.GitBranch))
	cloneArgs := []string{"clone", "--depth=1", "--branch", cfg.GitBranch, cfg.GitURL, workDir}
	if err := runCmd(ctx, logFn, "git", cloneArgs...); err != nil {
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
		workDir,
	}
	if err := runCmd(ctx, logFn, "docker", buildArgs...); err != nil {
		return "", fmt.Errorf("docker build: %w", err)
	}
	logFn(fmt.Sprintf("Successfully pushed %s", imageTag))
	return imageTag, nil
}

func runCmd(ctx context.Context, logFn func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
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
