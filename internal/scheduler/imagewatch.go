package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/gitrepo"
	"github.com/hoveychen/muvee/internal/store"
	"gopkg.in/yaml.v3"
)

const (
	settingImageWatchIntervalS  = "auto_deploy_image_watch_interval_seconds"
	defaultImageWatchInterval   = 10 * time.Minute
	minImageWatchInterval       = 60 * time.Second
	imageWatchPerImageTimeout   = 30 * time.Second
	imageWatchComposeFetchLimit = 1024 * 1024 // 1MB cap on compose file size
)

// StartImageWatcher launches the goroutine that periodically refreshes the
// known digest of every image referenced by an auto-deploy-enabled compose
// project, triggering a redeploy when any digest moves. Hosted-git compose
// projects are handled the same way as external ones — the only difference is
// where the docker-compose.yml is sourced from.
//
// The watcher is gated by SetImageWatchConfig — if the registry address is
// empty (e.g. registry not configured) it returns immediately. It also
// performs a one-shot connectivity probe against the registry; on failure
// the goroutine logs a warning and exits, so the rest of the server keeps
// running normally.
func (s *Scheduler) StartImageWatcher(ctx context.Context) {
	if s.registryAddr == "" {
		log.Println("image watcher disabled: REGISTRY_ADDR not configured")
		return
	}
	go s.runImageWatcher(ctx)
}

func (s *Scheduler) runImageWatcher(ctx context.Context) {
	if !s.probeRegistryReachable(ctx) {
		log.Printf("image watcher disabled: registry %q not reachable from control plane (this is fine if you keep the registry on a private network — the watcher will retry on next server restart)", s.registryAddr)
		return
	}
	log.Println("image watcher started")
	for {
		interval := s.currentImageWatchInterval(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			s.tickImageWatch(ctx)
		}
	}
}

func (s *Scheduler) currentImageWatchInterval(ctx context.Context) time.Duration {
	v, _ := s.store.GetSetting(ctx, settingImageWatchIntervalS)
	secs, err := strconv.Atoi(v)
	if err != nil || time.Duration(secs)*time.Second < minImageWatchInterval {
		return defaultImageWatchInterval
	}
	return time.Duration(secs) * time.Second
}

// probeRegistryReachable tries an unauthenticated HEAD on the registry's /v2/
// endpoint. A 200 or 401 both mean we can reach the registry (401 is the
// expected "needs auth" reply for a private registry). Anything else — DNS
// failure, refused connection, 5xx — is treated as unreachable.
func (s *Scheduler) probeRegistryReachable(ctx context.Context) bool {
	if s.registryAddr == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	probeURL := "https://" + s.registryAddr + "/v2/"
	req, err := http.NewRequestWithContext(cctx, http.MethodHead, probeURL, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized
}

func (s *Scheduler) tickImageWatch(ctx context.Context) {
	if v, _ := s.store.GetSetting(ctx, settingAutoDeployMasterEnabled); v == "false" {
		return
	}
	// Pass an empty git_source so both hosted and external compose projects
	// are returned. The list helper filters to project_type IN
	// ('deployment', 'compose') so deployment projects also come back; we
	// skip them in the loop because they don't have an image set to watch.
	projects, err := s.store.ListAutoDeployProjects(ctx, "")
	if err != nil {
		log.Printf("image watch: list projects: %v", err)
		return
	}
	for _, p := range projects {
		if p.ProjectType != store.ProjectTypeCompose {
			continue
		}
		if err := s.checkComposeImages(ctx, p); err != nil {
			log.Printf("image watch: project %q (%s): %v", p.Name, p.ID, err)
		}
	}
}

func (s *Scheduler) checkComposeImages(ctx context.Context, p *store.Project) error {
	composeData, err := s.fetchComposeFile(ctx, p)
	if err != nil {
		return fmt.Errorf("fetch compose: %w", err)
	}
	if len(composeData) == 0 {
		return nil // missing or empty file — nothing to watch
	}
	images, skipped := parseComposeImages(composeData)
	for _, img := range skipped {
		log.Printf("image watch: project %q skipped image %q (contains variable interpolation)", p.Name, img)
	}
	if len(images) == 0 {
		return nil
	}

	// Decode prior-recorded digests; tolerate corruption by treating it as
	// empty (re-seed on this tick instead of looping on the same JSON).
	prior := map[string]string{}
	if p.LastTrackedImageDigests != "" {
		_ = json.Unmarshal([]byte(p.LastTrackedImageDigests), &prior)
	}

	current := map[string]string{}
	changed := false
	for _, img := range images {
		digest, err := s.fetchImageDigest(ctx, img)
		if err != nil {
			// One image's lookup failure shouldn't poison the whole project —
			// keep the previous digest so we can compare on the next tick once
			// the registry recovers.
			log.Printf("image watch: project %q digest %q: %v", p.Name, img, err)
			if prev, ok := prior[img]; ok {
				current[img] = prev
			}
			continue
		}
		current[img] = digest
		if prev, ok := prior[img]; ok && prev != digest {
			changed = true
		}
	}

	// First-time seed: no prior map and no trigger; just persist current
	// digests so the next tick has something to compare against.
	if len(prior) == 0 {
		return s.persistDigests(ctx, p.ID, current)
	}

	if !changed {
		// Even with no change, persist if the *set* of images shifted (e.g.
		// the compose file added or removed an image string) so the map stays
		// in sync. New images alone never trigger a deploy — that's the git
		// poller's job, since a compose file edit always lands as a commit.
		if !sameKeys(prior, current) {
			return s.persistDigests(ctx, p.ID, current)
		}
		return nil
	}

	log.Printf("image watch: project %q image digest changed; triggering redeploy", p.Name)
	if _, err := s.TriggerDeployment(ctx, p.ID, "auto-image"); err != nil {
		return fmt.Errorf("trigger: %w", err)
	}
	return s.persistDigests(ctx, p.ID, current)
}

func (s *Scheduler) persistDigests(ctx context.Context, projectID uuid.UUID, m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal digests: %w", err)
	}
	return s.store.SetProjectLastTrackedImageDigests(ctx, projectID, string(b))
}

func sameKeys(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// fetchComposeFile returns the bytes of the project's docker-compose.yml at
// the project's tracked branch tip. For hosted repos it goes straight to the
// bare repo on disk; for external repos it shallow-clones to a tempdir.
func (s *Scheduler) fetchComposeFile(ctx context.Context, p *store.Project) ([]byte, error) {
	composePath := p.ComposeFilePath
	if composePath == "" {
		composePath = "docker-compose.yml"
	}
	branch := p.GitBranch
	if branch == "" {
		branch = "main"
	}

	if p.GitSource == store.GitSourceHosted {
		if s.gitRepoBasePath == "" {
			return nil, fmt.Errorf("hosted compose project but git repo base path is unset")
		}
		repoPath := gitrepo.RepoPath(s.gitRepoBasePath, p.ID)
		data, err := gitrepo.ReadBlobAtBranch(ctx, repoPath, branch, composePath)
		if err != nil {
			return nil, err
		}
		if len(data) > imageWatchComposeFetchLimit {
			return nil, fmt.Errorf("compose file exceeds %d bytes", imageWatchComposeFetchLimit)
		}
		return data, nil
	}

	// External repo: shallow clone into a tempdir, read the file, clean up.
	return s.fetchComposeFileExternal(ctx, p, branch, composePath)
}

func (s *Scheduler) fetchComposeFileExternal(ctx context.Context, p *store.Project, branch, composePath string) ([]byte, error) {
	if p.GitURL == "" {
		return nil, nil
	}

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
			return nil, fmt.Errorf("parse git url: %w", err)
		}
		u.User = url.UserPassword(gitUsername, gitToken)
		remote = u.String()
	}

	tmp, err := os.MkdirTemp("", "muvee-imagewatch-clone-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// shallow clone limits bandwidth (and registry rate-limits don't apply
	// here because this is the source-control side).
	cmd := exec.CommandContext(cctx, "git", "clone", "--depth=1", "--branch", branch, "--single-branch", remote, tmp)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if gitSSHKey != "" {
		keyFile, err := os.CreateTemp("", "muvee-imagewatch-sshkey-*")
		if err != nil {
			return nil, fmt.Errorf("temp ssh key: %w", err)
		}
		defer os.Remove(keyFile.Name())
		if err := os.WriteFile(keyFile.Name(), []byte(gitSSHKey), 0600); err != nil {
			return nil, fmt.Errorf("write ssh key: %w", err)
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf(
			"GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
			keyFile.Name(),
		))
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	composeFull := filepath.Join(tmp, composePath)
	info, err := os.Stat(composeFull)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.Size() > imageWatchComposeFetchLimit {
		return nil, fmt.Errorf("compose file exceeds %d bytes", imageWatchComposeFetchLimit)
	}
	return os.ReadFile(composeFull)
}

// parseComposeImages extracts every `services.<svc>.image` literal from a
// docker-compose YAML document. Returns the image strings AND any strings
// that were skipped because they contained variable interpolation (which
// the watcher cannot resolve statically).
func parseComposeImages(data []byte) (images []string, skipped []string) {
	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, svc := range doc.Services {
		img := strings.TrimSpace(svc.Image)
		if img == "" {
			continue
		}
		if seen[img] {
			continue
		}
		seen[img] = true
		if containsInterpolation(img) {
			skipped = append(skipped, img)
			continue
		}
		images = append(images, img)
	}
	return images, skipped
}

// containsInterpolation reports whether the image string contains a Compose
// variable reference. Compose accepts `${VAR}`, `${VAR:-default}`, and bare
// `$VAR` forms; resolving them statically would require a full env model.
func containsInterpolation(s string) bool {
	if !strings.Contains(s, "$") {
		return false
	}
	// "$$" is an escaped literal "$" in compose, not a variable.
	stripped := strings.ReplaceAll(s, "$$", "")
	return strings.Contains(stripped, "$")
}

// fetchImageDigest resolves the manifest digest of `image` by talking to the
// registry directly. The image's host is matched against the muvee
// private-registry address; on a match we authenticate with the configured
// REGISTRY_USER / REGISTRY_PASSWORD, otherwise we go anonymous (which is
// what most public images need).
func (s *Scheduler) fetchImageDigest(ctx context.Context, image string) (string, error) {
	ref, err := name.ParseReference(image)
	if err != nil {
		return "", fmt.Errorf("parse image: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, imageWatchPerImageTimeout)
	defer cancel()

	auth := s.authForImage(ref)
	digest, err := crane.Digest(image,
		crane.WithContext(cctx),
		crane.WithAuth(auth),
		crane.WithUserAgent("muvee-image-watcher"),
	)
	if err != nil {
		return "", err
	}
	return digest, nil
}

// authForImage decides which auth to present for a given image reference.
// muvee's private registry → REGISTRY_USER/PASSWORD; everything else →
// anonymous (crane handles docker-hub anonymous OAuth automatically).
func (s *Scheduler) authForImage(ref name.Reference) authn.Authenticator {
	if s.imageBelongsToOurRegistry(ref) && s.registryUser != "" && s.registryPassword != "" {
		return &authn.Basic{Username: s.registryUser, Password: s.registryPassword}
	}
	return authn.Anonymous
}

func (s *Scheduler) imageBelongsToOurRegistry(ref name.Reference) bool {
	if s.registryAddr == "" {
		return false
	}
	registryHost := ref.Context().RegistryStr()
	return hostMatchesRegistryAddr(registryHost, s.registryAddr)
}

// hostMatchesRegistryAddr returns true when the registry host parsed off an
// image reference matches our configured REGISTRY_ADDR. It tolerates an
// optional `:port` suffix on either side and case-insensitive hosts.
func hostMatchesRegistryAddr(refHost, configuredAddr string) bool {
	refHost = strings.ToLower(strings.TrimSpace(refHost))
	configuredAddr = strings.ToLower(strings.TrimSpace(configuredAddr))
	if refHost == "" || configuredAddr == "" {
		return false
	}
	if refHost == configuredAddr {
		return true
	}
	// Strip ports for a host-only comparison fallback (covers cases like
	// configured "registry.example.com:443" but ref host parsed as
	// "registry.example.com" by name.ParseReference).
	return stripPort(refHost) == stripPort(configuredAddr)
}

func stripPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}
