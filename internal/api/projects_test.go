package api

import (
	"encoding/json"
	"fmt"
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

// ─── updateProject merge behavior ──────────────────────────────────────────

// TestUpdateProject_PartialPayloadPreservesAuth verifies that a PUT request
// containing only config fields (no auth_required) does NOT reset auth_required
// to false. This reproduces the bug where saving from the Config tab could
// silently disable authentication.
func TestUpdateProject_PartialPayloadPreservesAuth(t *testing.T) {
	existing := &store.Project{
		Name:               "myapp",
		ProjectType:        store.ProjectTypeDeployment,
		DomainPrefix:       "myapp",
		AuthRequired:       true,
		AuthAllowedDomains: "company.com",
		AuthBypassPaths:    "/health\n/api/public/*",
	}

	// Simulate a request that only updates the description (doesn't mention auth fields).
	body := `{"name":"myapp","domain_prefix":"myapp","description":"updated desc"}`

	merged, err := mergeProjectUpdate(existing, strings.NewReader(body))
	if err != nil {
		t.Fatalf("mergeProjectUpdate error: %v", err)
	}

	if !merged.AuthRequired {
		t.Error("auth_required was reset to false; expected true (preserved from existing)")
	}
	if merged.AuthAllowedDomains != "company.com" {
		t.Errorf("auth_allowed_domains lost; got %q", merged.AuthAllowedDomains)
	}
	if merged.AuthBypassPaths != "/health\n/api/public/*" {
		t.Errorf("auth_bypass_paths lost; got %q", merged.AuthBypassPaths)
	}
	if merged.Description != "updated desc" {
		t.Errorf("description not updated; got %q", merged.Description)
	}
}

// TestUpdateProject_ExplicitFalseOverrides verifies that explicitly sending
// auth_required:false DOES disable auth (it's not ignored by the merge).
func TestUpdateProject_ExplicitFalseOverrides(t *testing.T) {
	existing := &store.Project{
		Name:         "myapp",
		ProjectType:  store.ProjectTypeDeployment,
		DomainPrefix: "myapp",
		AuthRequired: true,
	}

	body := `{"name":"myapp","domain_prefix":"myapp","auth_required":false}`
	merged, err := mergeProjectUpdate(existing, strings.NewReader(body))
	if err != nil {
		t.Fatalf("mergeProjectUpdate error: %v", err)
	}

	if merged.AuthRequired {
		t.Error("auth_required should be false when explicitly set in request")
	}
}

// ─── Traefik bypass router generation ──────────────���───────────────────────

func TestTraefikConfig_AuthBypassRouters(t *testing.T) {
	s := &Server{
		baseDomain:     "example.com",
		authServiceURL: "http://authservice:4181",
	}

	dep := &store.RunningDeploymentInfo{
		DomainPrefix:       "myapp",
		AuthRequired:       true,
		AuthAllowedDomains: "company.com",
		AuthBypassPaths:    "/health\n/api/public/*",
		HostIP:             "10.0.0.1",
		HostPort:           8080,
	}

	cfg := traefikDynamicConfig{
		HTTP: traefikHTTP{
			Routers:  make(map[string]traefikRouter),
			Services: make(map[string]traefikService),
		},
	}

	// Build config the same way handleTraefikConfig does.
	name := dep.DomainPrefix
	host := fmt.Sprintf("%s.%s", dep.DomainPrefix, s.baseDomain)
	backendURL := fmt.Sprintf("http://%s:%d", dep.HostIP, dep.HostPort)

	httpsRouter := traefikRouter{
		Rule:        fmt.Sprintf("Host(`%s`)", host),
		EntryPoints: []string{"websecure"},
		Service:     name,
		TLS: &traefikTLS{
			CertResolver: "letsencrypt",
			Domains:      []traefikTLSDomain{{Main: host}},
		},
	}

	if dep.AuthRequired {
		if cfg.HTTP.Middlewares == nil {
			cfg.HTTP.Middlewares = make(map[string]traefikMiddleware)
		}
		mwName := name + "-auth"
		verifyURL := fmt.Sprintf("%s/verify?project=%s", s.authServiceURL, dep.ProjectID)
		if dep.AuthAllowedDomains != "" {
			verifyURL += "&domains=" + dep.AuthAllowedDomains
		}
		cfg.HTTP.Middlewares[mwName] = traefikMiddleware{
			ForwardAuth: &traefikForwardAuth{
				Address:             verifyURL,
				AuthResponseHeaders: []string{"X-Forwarded-User", "X-Forwarded-User-Name", "X-Forwarded-User-Avatar", "X-Forwarded-User-Provider"},
				TrustForwardHeader:  true,
			},
		}
		httpsRouter.Middlewares = []string{mwName}

		if dep.AuthBypassPaths != "" {
			addBypassRouters(&cfg, name, host, httpsRouter.TLS, dep.AuthBypassPaths)
		}

		addDeviceFlowRouter(&cfg, name, host, httpsRouter.TLS)
	}

	cfg.HTTP.Routers[name] = httpsRouter
	cfg.HTTP.Services[name] = traefikService{
		LoadBalancer: traefikLB{
			Servers: []traefikServer{{URL: backendURL}},
		},
	}

	// Main router must have forwardAuth middleware.
	mainRouter, ok := cfg.HTTP.Routers["myapp"]
	if !ok {
		t.Fatal("main router not found")
	}
	if len(mainRouter.Middlewares) == 0 {
		t.Error("main router should have forwardAuth middleware")
	}

	// Bypass router for /health (exact path).
	bypass0, ok := cfg.HTTP.Routers["myapp-bypass-0"]
	if !ok {
		t.Fatal("bypass router myapp-bypass-0 not found")
	}
	if bypass0.Priority <= 0 {
		t.Errorf("bypass router should have positive priority, got %d", bypass0.Priority)
	}
	if len(bypass0.Middlewares) != 0 {
		t.Errorf("bypass router should have NO middlewares, got %v", bypass0.Middlewares)
	}
	expectedRule0 := "Host(`myapp.example.com`) && Path(`/health`)"
	if bypass0.Rule != expectedRule0 {
		t.Errorf("bypass-0 rule = %q, want %q", bypass0.Rule, expectedRule0)
	}
	if bypass0.Service != "myapp" {
		t.Errorf("bypass-0 service = %q, want %q", bypass0.Service, "myapp")
	}

	// Bypass router for /api/public/* (prefix match).
	bypass1, ok := cfg.HTTP.Routers["myapp-bypass-1"]
	if !ok {
		t.Fatal("bypass router myapp-bypass-1 not found")
	}
	expectedRule1 := "Host(`myapp.example.com`) && PathPrefix(`/api/public/`)"
	if bypass1.Rule != expectedRule1 {
		t.Errorf("bypass-1 rule = %q, want %q", bypass1.Rule, expectedRule1)
	}
	if len(bypass1.Middlewares) != 0 {
		t.Errorf("bypass-1 should have NO middlewares, got %v", bypass1.Middlewares)
	}

	// Verify JSON serialization includes priority and omits middlewares.
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"priority":100`) {
		t.Error("JSON output should contain priority:100 for bypass routers")
	}

	// Device-flow router should also exist for auth-protected deployments.
	dfRouter, ok := cfg.HTTP.Routers["myapp-device-flow"]
	if !ok {
		t.Fatal("device-flow router myapp-device-flow not found — addDeviceFlowRouter was not called for auth-protected deployment")
	}
	wantDFRule := "Host(`myapp.example.com`) && PathPrefix(`/_oauth/device`)"
	if dfRouter.Rule != wantDFRule {
		t.Errorf("device-flow rule = %q, want %q", dfRouter.Rule, wantDFRule)
	}
	if dfRouter.Service != deviceFlowServiceName {
		t.Errorf("device-flow service = %q, want %q", dfRouter.Service, deviceFlowServiceName)
	}
	if dfRouter.Priority < 200 {
		t.Errorf("device-flow priority should be >= 200, got %d", dfRouter.Priority)
	}
	if len(dfRouter.Middlewares) != 0 {
		t.Errorf("device-flow router should have NO middlewares (bypasses ForwardAuth), got %v", dfRouter.Middlewares)
	}
}
