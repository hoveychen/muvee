package deployer

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/datacache"
	"github.com/hoveychen/muvee/internal/store"
)

type Config struct {
	DeploymentID   string
	ProjectID      string
	DomainPrefix   string
	ImageTag       string
	AuthRequired   bool
	AuthDomains    string
	Datasets       []DatasetSpec
	BaseDomain     string
	AuthServiceURL string // e.g. http://muvee-authservice/verify
	RegistryAddr   string
}

type DatasetSpec struct {
	ID        string
	Name      string
	NFSPath   string
	Version   int64
	SizeBytes int64
	MountMode string
}

func Deploy(ctx context.Context, cfg Config, cache *datacache.Cache, st *store.Store, logFn func(string)) error {
	deploymentID, err := uuid.Parse(cfg.DeploymentID)
	if err != nil {
		return fmt.Errorf("invalid deployment id: %w", err)
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
		return fmt.Errorf("setup mounts: %w", err)
	}
	allMounts := append(depMounts, rwMounts...)

	// Build docker run command
	domain := cfg.DomainPrefix + "." + cfg.BaseDomain
	dockerArgs := []string{
		"run", "-d",
		"--name", oldContainer,
		"--restart", "unless-stopped",
		"--label", "traefik.enable=true",
		"--label", fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", cfg.DomainPrefix, domain),
		"--label", fmt.Sprintf("traefik.http.routers.%s.entrypoints=websecure", cfg.DomainPrefix),
		"--label", fmt.Sprintf("traefik.http.routers.%s.tls.certresolver=letsencrypt", cfg.DomainPrefix),
		"--label", fmt.Sprintf("traefik.http.routers.%s-http.rule=Host(`%s`)", cfg.DomainPrefix, domain),
		"--label", fmt.Sprintf("traefik.http.routers.%s-http.entrypoints=web", cfg.DomainPrefix),
		"--label", fmt.Sprintf("traefik.http.routers.%s-http.middlewares=redirect-to-https", cfg.DomainPrefix),
	}

	if cfg.AuthRequired && cfg.AuthServiceURL != "" {
		mwName := cfg.DomainPrefix + "-auth"
		verifyURL := fmt.Sprintf("%s?project=%s", cfg.AuthServiceURL, cfg.ProjectID)
		if cfg.AuthDomains != "" {
			verifyURL += "&domains=" + cfg.AuthDomains
		}
		dockerArgs = append(dockerArgs,
			"--label", fmt.Sprintf("traefik.http.middlewares.%s.forwardauth.address=%s", mwName, verifyURL),
			"--label", fmt.Sprintf("traefik.http.middlewares.%s.forwardauth.authResponseHeaders=X-Forwarded-User", mwName),
			"--label", fmt.Sprintf("traefik.http.routers.%s.middlewares=%s", cfg.DomainPrefix, mwName),
		)
	}

	for _, m := range allMounts {
		dockerArgs = append(dockerArgs, "-v", m)
	}

	dockerArgs = append(dockerArgs, cfg.ImageTag)
	logFn(fmt.Sprintf("Starting container: docker %s", strings.Join(dockerArgs, " ")))
	if err := runCmd(ctx, logFn, "docker", dockerArgs...); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}
	logFn("Container started successfully.")
	return nil
}

func runCmd(ctx context.Context, logFn func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logFn(string(out))
	}
	return err
}
