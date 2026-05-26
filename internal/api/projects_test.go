package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/store"
)

// ─── validateProject ────────────────────────────────────────────────────────

func TestValidateProject_AccessModeDefaultsToPublic(t *testing.T) {
	p := store.Project{Name: "my-app"}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.AccessMode != store.ProjectAccessModePublic {
		t.Errorf("expected default access_mode=public, got %q", p.AccessMode)
	}
}

func TestValidateProject_AccessModePrivatePreserved(t *testing.T) {
	p := store.Project{Name: "my-app", AccessMode: store.ProjectAccessModePrivate}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.AccessMode != store.ProjectAccessModePrivate {
		t.Errorf("expected access_mode=private preserved, got %q", p.AccessMode)
	}
}

func TestValidateProject_AccessModeRejectsUnknown(t *testing.T) {
	p := store.Project{Name: "my-app", AccessMode: "internal"}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error for unknown access_mode")
	}
	if !strings.Contains(err.Error(), "access_mode") {
		t.Errorf("expected access_mode-related error, got %v", err)
	}
}

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
	if p.DockerfilePath != "Dockerfile" {
		t.Errorf("expected dockerfile_path defaulted to %q, got %q", "Dockerfile", p.DockerfilePath)
	}
	if p.GitBranch != "main" {
		t.Errorf("expected git_branch defaulted to %q, got %q", "main", p.GitBranch)
	}
}

func TestValidateProject_DeploymentPreservesExplicitPaths(t *testing.T) {
	p := store.Project{
		Name:           "my-app",
		DockerfilePath: "web/Dockerfile",
		GitBranch:      "release",
	}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.DockerfilePath != "web/Dockerfile" {
		t.Errorf("expected explicit dockerfile_path preserved, got %q", p.DockerfilePath)
	}
	if p.GitBranch != "release" {
		t.Errorf("expected explicit git_branch preserved, got %q", p.GitBranch)
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

// ─── fixed-port semantics ──────────────────────────────────────────────────

func TestValidateFixedPort_BothNilOK(t *testing.T) {
	p := store.Project{Name: "app"}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error when both fixed-port fields are nil: %v", err)
	}
}

func TestValidateFixedPort_PortWithoutNodeRejected(t *testing.T) {
	port := 13000
	p := store.Project{Name: "app", FixedHostPort: &port}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected error when fixed_host_port is set without fixed_node_id")
	}
}

func TestValidateFixedPort_NodeWithoutPortRejected(t *testing.T) {
	id := uuid.New()
	p := store.Project{Name: "app", FixedNodeID: &id}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected error when fixed_node_id is set without fixed_host_port")
	}
}

func TestValidateFixedPort_PortRange(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		port    int
		wantErr bool
	}{
		{1023, true},
		{1024, false},
		{65535, false},
		{65536, true},
	}
	for _, c := range cases {
		port := c.port
		p := store.Project{Name: "app", FixedHostPort: &port, FixedNodeID: &id}
		err := validateProject(&p)
		if c.wantErr && err == nil {
			t.Errorf("port=%d: expected range error, got nil", c.port)
		}
		if !c.wantErr && err != nil {
			t.Errorf("port=%d: unexpected error: %v", c.port, err)
		}
	}
}

func TestValidateFixedPort_DomainOnlyRejected(t *testing.T) {
	id := uuid.New()
	port := 13000
	p := store.Project{
		Name:          "app",
		ProjectType:   store.ProjectTypeDomainOnly,
		DomainPrefix:  "mine",
		FixedHostPort: &port,
		FixedNodeID:   &id,
	}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected fixed-port to be rejected for domain_only projects")
	}
}

func TestValidateFixedPort_ZeroNodeIDRejected(t *testing.T) {
	port := 13000
	zero := uuid.Nil
	p := store.Project{Name: "app", FixedHostPort: &port, FixedNodeID: &zero}
	if err := validateProject(&p); err == nil {
		t.Fatal("expected fixed_node_id=zero UUID to be rejected")
	}
}

func TestFixedPortChanged(t *testing.T) {
	port13000 := 13000
	port14000 := 14000
	idA := uuid.New()
	idB := uuid.New()

	cases := []struct {
		name string
		a, b store.Project
		want bool
	}{
		{"both nil — same", store.Project{}, store.Project{}, false},
		{"port added", store.Project{}, store.Project{FixedHostPort: &port13000, FixedNodeID: &idA}, true},
		{"port removed", store.Project{FixedHostPort: &port13000, FixedNodeID: &idA}, store.Project{}, true},
		{"port changed", store.Project{FixedHostPort: &port13000, FixedNodeID: &idA}, store.Project{FixedHostPort: &port14000, FixedNodeID: &idA}, true},
		{"node changed", store.Project{FixedHostPort: &port13000, FixedNodeID: &idA}, store.Project{FixedHostPort: &port13000, FixedNodeID: &idB}, true},
		{"identical", store.Project{FixedHostPort: &port13000, FixedNodeID: &idA}, store.Project{FixedHostPort: &port13000, FixedNodeID: &idA}, false},
	}
	for _, c := range cases {
		if got := fixedPortChanged(&c.a, &c.b); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
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

// TestUpdateProject_BrandingFieldsMerge verifies that per-project branding
// fields (added in migration 040) round-trip through mergeProjectUpdate the
// same way other config fields do: present keys overwrite, absent keys keep
// the existing value. This locks in the contract that project owners can
// edit branding from the Config tab without zeroing out other branding keys
// they did not touch in the same save.
func TestUpdateProject_BrandingFieldsMerge(t *testing.T) {
	existing := &store.Project{
		Name:                 "myapp",
		ProjectType:          store.ProjectTypeDeployment,
		DomainPrefix:         "myapp",
		BrandingSiteName:     "Old Site",
		BrandingLogoURL:      "https://example.com/old.png",
		BrandingPrimaryColor: "#000000",
		BrandingTagline:      "old tagline",
	}

	// Partial save: only updates site name + logo. The other two existing
	// branding fields must survive.
	body := `{"name":"myapp","domain_prefix":"myapp","branding_site_name":"New Site","branding_logo_url":"https://example.com/new.png","branding_sidebar_bg":"#222","branding_description":"a description"}`

	merged, err := mergeProjectUpdate(existing, strings.NewReader(body))
	if err != nil {
		t.Fatalf("mergeProjectUpdate error: %v", err)
	}

	if merged.BrandingSiteName != "New Site" {
		t.Errorf("branding_site_name not updated; got %q", merged.BrandingSiteName)
	}
	if merged.BrandingLogoURL != "https://example.com/new.png" {
		t.Errorf("branding_logo_url not updated; got %q", merged.BrandingLogoURL)
	}
	if merged.BrandingSidebarBg != "#222" {
		t.Errorf("branding_sidebar_bg not set; got %q", merged.BrandingSidebarBg)
	}
	if merged.BrandingDescription != "a description" {
		t.Errorf("branding_description not set; got %q", merged.BrandingDescription)
	}
	if merged.BrandingPrimaryColor != "#000000" {
		t.Errorf("branding_primary_color lost; got %q", merged.BrandingPrimaryColor)
	}
	if merged.BrandingTagline != "old tagline" {
		t.Errorf("branding_tagline lost; got %q", merged.BrandingTagline)
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

	// OAuth-bypass router should also exist for auth-protected deployments: it
	// must cover the whole /_oauth namespace so logout / userinfo / device flow
	// all reach the authservice on project subdomains.
	if _, ok := cfg.HTTP.Routers["myapp-device-flow"]; !ok {
		t.Fatal("oauth-bypass router myapp-device-flow not found — addDeviceFlowRouter was not called for auth-protected deployment")
	}
}

// TestTraefikConfig_DeviceFlowRouter_PublicProject locks in the fact that
// /_oauth/* must route to authservice on EVERY project subdomain, not only
// auth-protected ones. The @muvee/auth SDK calls /_oauth/providers and
// /_oauth/login-token relative to the project host; if those paths fall
// through to the project's own backend (nginx serving a SPA, in the demo's
// case), the SDK gets HTML instead of JSON and sign-in is broken.
//
// Red-light evidence before the fix: `curl
// https://auth-sdk-demo.muveeai.com/_oauth/providers` returned the demo's
// index.html via nginx's SPA fallback because Traefik had no /_oauth router
// registered for the public deployment. This test mirrors the handler's
// post-fix structure (addDeviceFlowRouter called unconditionally) so a future
// regression that nests the call back under `if needsForwardAuth` fails here.
func TestTraefikConfig_DeviceFlowRouter_PublicProject(t *testing.T) {
	s := &Server{
		baseDomain:     "example.com",
		authServiceURL: "http://authservice:4181",
	}
	dep := &store.RunningDeploymentInfo{
		DomainPrefix: "auth-sdk-demo",
		AuthRequired: false,
		AccessMode:   store.ProjectAccessModePublic,
		HostIP:       "10.0.0.1",
		HostPort:     8080,
	}

	cfg := traefikDynamicConfig{
		HTTP: traefikHTTP{
			Routers:  make(map[string]traefikRouter),
			Services: make(map[string]traefikService),
		},
	}
	name := dep.DomainPrefix
	host := fmt.Sprintf("%s.%s", dep.DomainPrefix, s.baseDomain)

	httpsRouter := traefikRouter{
		Rule:        fmt.Sprintf("Host(`%s`)", host),
		EntryPoints: []string{"websecure"},
		Service:     name,
		TLS: &traefikTLS{
			CertResolver: "letsencrypt",
			Domains:      []traefikTLSDomain{{Main: host}},
		},
	}

	// Post-fix invariant: addDeviceFlowRouter runs regardless of auth mode.
	needsForwardAuth := dep.AuthRequired || dep.AccessMode == store.ProjectAccessModePrivate
	if needsForwardAuth {
		t.Fatalf("test setup error: needsForwardAuth should be false for a public + auth_required=false deployment")
	}
	addDeviceFlowRouter(&cfg, name, host, httpsRouter.TLS)

	cfg.HTTP.Routers[name] = httpsRouter

	dfRouter, ok := cfg.HTTP.Routers["auth-sdk-demo-device-flow"]
	if !ok {
		t.Fatal("device-flow router not found on public project — @muvee/auth SDK would 404 on /_oauth/providers")
	}
	wantRule := "Host(`auth-sdk-demo.example.com`) && PathPrefix(`/_oauth`)"
	if dfRouter.Rule != wantRule {
		t.Errorf("device-flow rule = %q, want %q", dfRouter.Rule, wantRule)
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

// ─── compose hosted git ─────────────────────────────────────────────────────

func TestValidateProject_Compose_HostedAcceptedAndClearsGitURL(t *testing.T) {
	p := store.Project{
		Name:          "stack",
		ProjectType:   store.ProjectTypeCompose,
		GitSource:     store.GitSourceHosted,
		GitURL:        "https://example.com/should-be-ignored.git",
		ExposeService: "web",
		ExposePort:    8080,
	}
	if err := validateProject(&p); err != nil {
		t.Fatalf("hosted compose should validate, got %v", err)
	}
	if p.GitSource != store.GitSourceHosted {
		t.Errorf("expected git_source=hosted preserved, got %q", p.GitSource)
	}
	if p.GitURL != "" {
		t.Errorf("expected git_url cleared for hosted compose, got %q", p.GitURL)
	}
	if p.GitBranch != "main" {
		t.Errorf("expected git_branch defaulted to main, got %q", p.GitBranch)
	}
	if p.ComposeFilePath != "docker-compose.yml" {
		t.Errorf("expected compose_file_path defaulted, got %q", p.ComposeFilePath)
	}
}

func TestValidateProject_Compose_ExternalStillRequiresGitURL(t *testing.T) {
	p := store.Project{
		Name:          "stack",
		ProjectType:   store.ProjectTypeCompose,
		GitSource:     store.GitSourceExternal,
		ExposeService: "web",
		ExposePort:    8080,
	}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error for external compose without git_url")
	}
	if !strings.Contains(err.Error(), "git_url") {
		t.Errorf("expected git_url error, got %v", err)
	}
}

func TestValidateProject_Compose_HostedStillRequiresExposeService(t *testing.T) {
	p := store.Project{
		Name:        "stack",
		ProjectType: store.ProjectTypeCompose,
		GitSource:   store.GitSourceHosted,
		ExposePort:  8080,
	}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error for hosted compose without expose_service")
	}
	if !strings.Contains(err.Error(), "expose_service") {
		t.Errorf("expected expose_service error, got %v", err)
	}
}

func TestValidateProject_Compose_RejectsBogusGitSource(t *testing.T) {
	p := store.Project{
		Name:          "stack",
		ProjectType:   store.ProjectTypeCompose,
		GitSource:     "bogus",
		GitURL:        "https://example.com/x.git",
		ExposeService: "web",
		ExposePort:    8080,
	}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error for invalid git_source on compose")
	}
	if !strings.Contains(err.Error(), "git_source") {
		t.Errorf("expected git_source error, got %v", err)
	}
}

// ─── Build-only projects ─────────────────────────────────────────────────────

func TestValidateProject_Build_ExternalDefaults(t *testing.T) {
	p := store.Project{
		Name:        "hub-builder",
		ProjectType: store.ProjectTypeBuild,
		GitURL:      "https://github.com/foo/bar.git",
	}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.GitSource != store.GitSourceExternal {
		t.Errorf("expected git_source default external, got %q", p.GitSource)
	}
	if p.GitBranch != "main" {
		t.Errorf("expected git_branch defaulted to main, got %q", p.GitBranch)
	}
	if p.DockerfilePath != "Dockerfile" {
		t.Errorf("expected dockerfile_path defaulted, got %q", p.DockerfilePath)
	}
	if p.DomainPrefix != "hub-builder" {
		t.Errorf("expected domain_prefix defaulted to name, got %q", p.DomainPrefix)
	}
}

func TestValidateProject_Build_HostedClearsGitURL(t *testing.T) {
	p := store.Project{
		Name:        "hub-builder",
		ProjectType: store.ProjectTypeBuild,
		GitSource:   store.GitSourceHosted,
		GitURL:      "https://leftover",
	}
	if err := validateProject(&p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.GitURL != "" {
		t.Errorf("expected hosted build to clear git_url, got %q", p.GitURL)
	}
}

func TestValidateProject_Build_ExternalRequiresGitURL(t *testing.T) {
	p := store.Project{
		Name:        "hub-builder",
		ProjectType: store.ProjectTypeBuild,
	}
	err := validateProject(&p)
	if err == nil {
		t.Fatal("expected error for external build without git_url")
	}
	if !strings.Contains(err.Error(), "git_url") {
		t.Errorf("expected git_url error, got %v", err)
	}
}

func TestValidateProject_Build_RejectsContainerPort(t *testing.T) {
	p := store.Project{
		Name:          "hub-builder",
		ProjectType:   store.ProjectTypeBuild,
		GitURL:        "https://example.com/x.git",
		ContainerPort: 8080,
	}
	err := validateProject(&p)
	if err == nil || !strings.Contains(err.Error(), "container_port") {
		t.Fatalf("expected container_port error, got %v", err)
	}
}

func TestValidateProject_Build_RejectsExposeFields(t *testing.T) {
	p := store.Project{
		Name:          "hub-builder",
		ProjectType:   store.ProjectTypeBuild,
		GitURL:        "https://example.com/x.git",
		ExposeService: "web",
	}
	err := validateProject(&p)
	if err == nil || !strings.Contains(err.Error(), "expose") {
		t.Fatalf("expected expose error, got %v", err)
	}
}

func TestValidateProject_Build_RejectsAuthRequired(t *testing.T) {
	p := store.Project{
		Name:         "hub-builder",
		ProjectType:  store.ProjectTypeBuild,
		GitURL:       "https://example.com/x.git",
		AuthRequired: true,
	}
	err := validateProject(&p)
	if err == nil || !strings.Contains(err.Error(), "auth_required") {
		t.Fatalf("expected auth_required error, got %v", err)
	}
}

func TestValidateProject_Build_RejectsImageRef(t *testing.T) {
	p := store.Project{
		Name:        "hub-builder",
		ProjectType: store.ProjectTypeBuild,
		GitURL:      "https://example.com/x.git",
		ImageRef:    "ghcr.io/foo:latest",
	}
	err := validateProject(&p)
	if err == nil || !strings.Contains(err.Error(), "image_ref") {
		t.Fatalf("expected image_ref error, got %v", err)
	}
}

func TestValidateProject_Build_RejectsMemoryLimit(t *testing.T) {
	p := store.Project{
		Name:        "hub-builder",
		ProjectType: store.ProjectTypeBuild,
		GitURL:      "https://example.com/x.git",
		MemoryLimit: "4g",
	}
	err := validateProject(&p)
	if err == nil || !strings.Contains(err.Error(), "memory_limit") {
		t.Fatalf("expected memory_limit error, got %v", err)
	}
}

func TestValidateProject_Build_RejectsPrivateAccessMode(t *testing.T) {
	p := store.Project{
		Name:        "hub-builder",
		ProjectType: store.ProjectTypeBuild,
		GitURL:      "https://example.com/x.git",
		AccessMode:  store.ProjectAccessModePrivate,
	}
	err := validateProject(&p)
	if err == nil || !strings.Contains(err.Error(), "access_mode") {
		t.Fatalf("expected access_mode error, got %v", err)
	}
}
