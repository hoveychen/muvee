package scheduler

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hoveychen/muvee/internal/store"
)

const (
	settingAutoDeployMasterEnabled  = "auto_deploy_master_enabled"
	settingAutoDeployPollIntervalS  = "auto_deploy_poll_interval_seconds"
	defaultAutoDeployPollInterval   = 60 * time.Second
	minAutoDeployPollInterval       = 10 * time.Second
	autoDeployFetchTimeout          = 30 * time.Second
)

// StartAutoDeployPoller spawns the goroutine that periodically polls every
// project with auto_deploy_enabled=true AND git_source=external, comparing
// the live remote HEAD against last_tracked_commit_sha. When they differ it
// calls TriggerDeployment and records the new SHA so the same commit is not
// redeployed on the next tick. Hosted projects use a separate push-driven
// path and are skipped here.
func (s *Scheduler) StartAutoDeployPoller(ctx context.Context) {
	go s.runAutoDeployPoller(ctx)
}

func (s *Scheduler) runAutoDeployPoller(ctx context.Context) {
	log.Println("auto-deploy poller started")
	for {
		interval := s.currentAutoDeployInterval(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			s.tickAutoDeploy(ctx)
		}
	}
}

func (s *Scheduler) currentAutoDeployInterval(ctx context.Context) time.Duration {
	v, _ := s.store.GetSetting(ctx, settingAutoDeployPollIntervalS)
	secs, err := strconv.Atoi(v)
	if err != nil || time.Duration(secs)*time.Second < minAutoDeployPollInterval {
		return defaultAutoDeployPollInterval
	}
	return time.Duration(secs) * time.Second
}

func (s *Scheduler) tickAutoDeploy(ctx context.Context) {
	if v, _ := s.store.GetSetting(ctx, settingAutoDeployMasterEnabled); v == "false" {
		return
	}
	projects, err := s.store.ListAutoDeployProjects(ctx, store.GitSourceExternal)
	if err != nil {
		log.Printf("auto-deploy: list projects: %v", err)
		return
	}
	for _, p := range projects {
		if err := s.checkAndTriggerExternal(ctx, p); err != nil {
			log.Printf("auto-deploy: project %q (%s): %v", p.Name, p.ID, err)
		}
	}
}

func (s *Scheduler) checkAndTriggerExternal(ctx context.Context, p *store.Project) error {
	if p.GitURL == "" {
		return nil
	}
	branch := p.GitBranch
	if branch == "" {
		branch = "main"
	}
	sha, err := s.fetchExternalRemoteHead(ctx, p, branch)
	if err != nil {
		return fmt.Errorf("ls-remote: %w", err)
	}
	if sha == "" {
		return fmt.Errorf("branch %q not present on remote", branch)
	}
	if sha == p.LastTrackedCommitSHA {
		return nil
	}
	log.Printf("auto-deploy: project %q new commit %s (was %q)", p.Name, sha, p.LastTrackedCommitSHA)
	if _, err := s.TriggerDeployment(ctx, p.ID, "auto-poll"); err != nil {
		return fmt.Errorf("trigger: %w", err)
	}
	if err := s.store.SetProjectLastTrackedCommitSHA(ctx, p.ID, sha); err != nil {
		return fmt.Errorf("record sha: %w", err)
	}
	return nil
}

// fetchExternalRemoteHead returns the commit SHA at the tip of `branch` on the
// project's remote. Mirrors the builder's auth handling (HTTPS user/token
// rewriting + GIT_SSH_COMMAND with a temp keyfile) so private repos work.
func (s *Scheduler) fetchExternalRemoteHead(ctx context.Context, p *store.Project, branch string) (string, error) {
	secrets, _ := s.store.GetProjectSecretsDecrypted(ctx, p.ID)
	var gitSSHKey, gitUsername, gitToken string
	for _, sec := range secrets {
		if !sec.UseForGit {
			continue
		}
		switch sec.SecretType {
		case store.SecretTypeSSHKey:
			gitSSHKey = sec.PlainValue
		case store.SecretTypePassword:
			gitUsername = sec.GitUsername
			gitToken = sec.PlainValue
		}
	}

	remote := p.GitURL
	if gitUsername != "" && gitToken != "" && strings.HasPrefix(remote, "https://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("parse git url: %w", err)
		}
		u.User = url.UserPassword(gitUsername, gitToken)
		remote = u.String()
	}

	cctx, cancel := context.WithTimeout(ctx, autoDeployFetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", "ls-remote", "--quiet", remote, "refs/heads/"+branch)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never block on credential prompt
	)
	if gitSSHKey != "" {
		keyFile, err := os.CreateTemp("", "muvee-autodeploy-sshkey-*")
		if err != nil {
			return "", fmt.Errorf("temp ssh key: %w", err)
		}
		defer os.Remove(keyFile.Name())
		if err := os.WriteFile(keyFile.Name(), []byte(gitSSHKey), 0600); err != nil {
			return "", fmt.Errorf("write ssh key: %w", err)
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf(
			"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
			keyFile.Name(),
		))
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return parseLsRemoteHead(string(out)), nil
}

// parseLsRemoteHead extracts the commit SHA from `git ls-remote --quiet <url>
// refs/heads/<branch>` output. The expected line format is "<sha>\trefs/heads/<branch>".
// Lines whose second column doesn't look like a ref (e.g. stray "warning:"
// notices that git emits even with --quiet) are skipped. Returns an empty
// string when no matching ref line is found.
func parseLsRemoteHead(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if !strings.HasPrefix(fields[1], "refs/") {
			continue
		}
		return fields[0]
	}
	return ""
}
