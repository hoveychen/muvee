package api

import (
	"strings"
	"testing"

	"github.com/hoveychen/muvee/internal/store"
)

// ─── validateProject ────────────────────────────────────────────────────────

func TestValidateProject_DeploymentDefaults(t *testing.T) {
	p := store.Project{Name: "my-app"}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ProjectType != store.ProjectTypeDeployment {
		t.Errorf("expected default project_type=deployment, got %q", p.ProjectType)
	}
	if p.GitSource != store.GitSourceExternal {
		t.Errorf("expected default git_source=external, got %q", p.GitSource)
	}
	if p.DomainPrefix != "my-app" {
		t.Errorf("expected domain_prefix defaulted to name, got %q", p.DomainPrefix)
	}
}

func TestValidateProject_EmptyName(t *testing.T) {
	p := store.Project{}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestValidateProject_InvalidProjectType(t *testing.T) {
	p := store.Project{Name: "app", ProjectType: "bogus"}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected error for unknown project_type")
	}
}

func TestValidateProject_DomainOnly_RequiresDomainPrefix(t *testing.T) {
	p := store.Project{Name: "app", ProjectType: store.ProjectTypeDomainOnly}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error when domain_prefix is missing")
	}
	if !strings.Contains(err.Error(), "domain_prefix is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateProject_DomainOnly_RejectsTPrefix(t *testing.T) {
	p := store.Project{
		Name:         "app",
		ProjectType:  store.ProjectTypeDomainOnly,
		DomainPrefix: "t-mine",
	}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error for t- prefix")
	}
	if !strings.Contains(err.Error(), "tunnel namespace") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateProject_DomainOnly_RejectsGitURL(t *testing.T) {
	p := store.Project{
		Name:         "app",
		ProjectType:  store.ProjectTypeDomainOnly,
		DomainPrefix: "mine",
		GitURL:       "https://github.com/foo/bar.git",
	}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected error for git_url on domain_only project")
	}
}

func TestValidateProject_DomainOnly_RejectsHostedGitSource(t *testing.T) {
	p := store.Project{
		Name:         "app",
		ProjectType:  store.ProjectTypeDomainOnly,
		DomainPrefix: "mine",
		GitSource:    store.GitSourceHosted,
	}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected error for git_source=hosted on domain_only project")
	}
}

func TestValidateProject_DomainOnly_ZeroesDeploymentFields(t *testing.T) {
	p := store.Project{
		Name:            "app",
		ProjectType:     store.ProjectTypeDomainOnly,
		DomainPrefix:    "mine",
		GitBranch:       "main",
		DockerfilePath:  "Dockerfile",
		ContainerPort:   8080,
		MemoryLimit:     "512m",
		VolumeMountPath: "/data",
	}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.GitBranch != "" || p.DockerfilePath != "" || p.ContainerPort != 0 ||
		p.MemoryLimit != "" || p.VolumeMountPath != "" || p.GitSource != "" {
		t.Errorf("deployment fields should be zeroed for domain_only: %+v", p)
	}
}

func TestValidateProject_DomainOnly_RejectsReservedPrefix(t *testing.T) {
	// Reserved prefixes share the same validation path as regular projects.
	for prefix := range reservedDomainPrefixes {
		p := store.Project{
			Name:         "app",
			ProjectType:  store.ProjectTypeDomainOnly,
			DomainPrefix: prefix,
		}
		if err := validateProject(&p); err == nil {
			t.Errorf("expected reserved prefix %q to be rejected", prefix)
		}
		break // one sample is enough
	}
}

func TestValidateProject_DomainOnly_Success(t *testing.T) {
	p := store.Project{
		Name:         "app",
		ProjectType:  store.ProjectTypeDomainOnly,
		DomainPrefix: "mine",
	}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
