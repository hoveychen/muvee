package api

import (
	"encoding/json"
	"fmt"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/gitrepo"
	"github.com/hoveychen/muvee/internal/monitor"
	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

type Server struct {
	store              *store.Store
	auth               *auth.Service
	sched              *scheduler.Scheduler
	monitor            *monitor.Monitor
	baseDomain         string
	authServiceURL     string   // base URL of muvee-authservice, e.g. http://muvee-authservice:4181
	agentSecret        string   // shared secret for agent ↔ server authentication
	registryAddr       string   // address of the Docker registry distributed to agents
	registryUser       string   // registry basic-auth username distributed to agents
	registryPassword   string   // registry basic-auth password distributed to agents
	volumeNFSBasePath  string   // base NFS path for project workspace volumes
	datasetNFSBasePath string   // base NFS path for dataset files
	gitRepoBasePath    string   // base path for hosted bare git repos
	brandingDir        string   // directory for uploaded branding assets (logo, favicon)
	cliPending         sync.Map // state -> cli_port (string)
	oauthPending       sync.Map // state -> provider name (string); fallback when cookie is missing
}

type ServerConfig struct {
	BaseDomain         string
	AuthServiceURL     string
	AgentSecret        string
	RegistryAddr       string
	RegistryUser       string
	RegistryPassword   string
	VolumeNFSBasePath  string
	DatasetNFSBasePath string
	GitRepoBasePath    string
	BrandingDir        string
}

func NewServer(st *store.Store, authSvc *auth.Service, sched *scheduler.Scheduler, mon *monitor.Monitor, cfg ServerConfig) *Server {
	if cfg.AuthServiceURL == "" {
		cfg.AuthServiceURL = "http://muvee-authservice:4181"
	}
	return &Server{
		store:              st,
		auth:               authSvc,
		sched:              sched,
		monitor:            mon,
		baseDomain:         cfg.BaseDomain,
		authServiceURL:     cfg.AuthServiceURL,
		agentSecret:        cfg.AgentSecret,
		registryAddr:       cfg.RegistryAddr,
		registryUser:       cfg.RegistryUser,
		registryPassword:   cfg.RegistryPassword,
		volumeNFSBasePath:  cfg.VolumeNFSBasePath,
		datasetNFSBasePath: cfg.DatasetNFSBasePath,
		gitRepoBasePath:    cfg.GitRepoBasePath,
		brandingDir:        cfg.BrandingDir,
	}
}

// agentSecretMiddleware rejects requests that do not carry the correct X-Agent-Secret header.
// When no secret is configured the middleware passes all requests through (dev/test mode).
func (s *Server) agentSecretMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.agentSecret != "" && r.Header.Get("X-Agent-Secret") != s.agentSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	// Auth – provider-agnostic routes (/auth/{provider}/login and /auth/{provider}/callback)
	r.Get("/auth/{provider}/login", s.handleProviderLogin)
	r.Get("/auth/{provider}/callback", s.handleProviderCallback)
	r.Post("/auth/logout", s.handleLogout)

	// CLI device-flow auth
	r.Get("/auth/cli/login", s.handleCLILogin)

	// Public: list enabled auth providers (used by frontend to render login buttons)
	r.Get("/api/auth/providers", s.handleListProviders)

	// Public skill document for Claude
	r.Get("/api/skill", s.handleSkill)

	// Traefik HTTP provider – no auth, consumed only by Traefik on the internal network
	r.Get("/api/traefik/config", s.handleTraefikConfig)

	// Public community feed – no auth required
	r.Get("/api/public/projects", s.handlePublicProjects)

	// Public system settings (branding, onboarding state) – no auth required
	r.Get("/api/public/settings", s.handleGetPublicSettings)

	// Public branding assets (uploaded logo/favicon) – no auth required
	r.Get("/api/public/branding/{filename}", s.handleServeBranding)

	// Git Smart HTTP protocol – uses its own Basic Auth (API tokens)
	if s.gitRepoBasePath != "" {
		r.Handle("/git/*", gitrepo.HTTPHandler(s.gitRepoBasePath, s.gitHTTPAuth))
	}

	// Protected
	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		r.Get("/api/me", s.handleMe)
		r.Get("/api/runtime/config", s.handleRuntimeConfig)

		// Project-scoped API Tokens
		r.Get("/api/projects/{id}/tokens", s.listProjectTokens)
		r.Post("/api/projects/{id}/tokens", s.createProjectToken)
		r.Delete("/api/projects/{id}/tokens/{tokenId}", s.deleteProjectToken)

		// Secrets
		r.Get("/api/secrets", s.listSecrets)
		r.Post("/api/secrets", s.createSecret)
		r.Delete("/api/secrets/{id}", s.deleteSecret)

		// Projects
		r.Get("/api/projects", s.listProjects)
		r.Post("/api/projects", s.createProject)
		r.Get("/api/projects/{id}", s.getProject)
		r.Put("/api/projects/{id}", s.updateProject)
		r.Delete("/api/projects/{id}", s.deleteProject)
		r.Get("/api/projects/{id}/datasets", s.getProjectDatasets)
		r.Put("/api/projects/{id}/datasets", s.setProjectDatasets)
		r.Get("/api/projects/{id}/secrets", s.getProjectSecrets)
		r.Put("/api/projects/{id}/secrets", s.setProjectSecrets)
		r.Post("/api/projects/{id}/deploy", s.triggerDeploy)
		r.Get("/api/projects/{id}/deployments", s.listDeployments)
		r.Get("/api/projects/{id}/metrics", s.getProjectMetrics)
		r.Get("/api/projects/{id}/workspace", s.workspaceList)
		r.Get("/api/projects/{id}/workspace/download", s.workspaceDownload)
		r.Post("/api/projects/{id}/workspace/upload", s.workspaceUpload)
		r.Delete("/api/projects/{id}/workspace", s.workspaceDelete)

		// Hosted git repository browser
		r.Get("/api/projects/{id}/repo/tree", s.repoTree)
		r.Get("/api/projects/{id}/repo/blob", s.repoBlob)
		r.Get("/api/projects/{id}/repo/commits", s.repoCommits)
		r.Get("/api/projects/{id}/repo/branches", s.repoBranches)

		// Datasets
		r.Get("/api/datasets", s.listDatasets)
		r.Post("/api/datasets", s.createDataset)
		r.Get("/api/datasets/{id}", s.getDataset)
		r.Put("/api/datasets/{id}", s.updateDataset)
		r.Delete("/api/datasets/{id}", s.deleteDataset)
		r.Post("/api/datasets/{id}/scan", s.scanDataset)
		r.Get("/api/datasets/{id}/snapshots", s.listSnapshots)
		r.Get("/api/datasets/{id}/history", s.listFileHistory)

		// Nodes & admin-only operations
		r.Group(func(r chi.Router) {
			r.Use(auth.AdminOnly)
			r.Get("/api/nodes", s.listNodes)
			r.Delete("/api/nodes/{id}", s.deleteNode)
			r.Get("/api/nodes/{id}/metrics", s.getNodeMetrics)
			r.Get("/api/users", s.listUsers)
			r.Put("/api/users/{id}/role", s.setUserRole)
			// System settings (admin-only read/write)
			r.Get("/api/admin/settings", s.handleGetAdminSettings)
			r.Put("/api/admin/settings", s.handleUpdateAdminSettings)
			r.Post("/api/admin/branding/upload", s.handleBrandingUpload)
			// Server-side health checks
			r.Get("/api/admin/health", s.handleGetSystemHealth)
		})
	})

	// Agent endpoints – protected by shared secret (X-Agent-Secret header)
	r.Group(func(r chi.Router) {
		r.Use(s.agentSecretMiddleware)
		r.Get("/api/agent/tasks", s.pollTasks)
		r.Post("/api/agent/tasks/{id}/complete", s.completeTask)
		r.Post("/api/agent/register", s.registerNode)
		r.Get("/api/agent/config", s.handleAgentConfig)
		r.Post("/api/agent/container-statuses", s.handleContainerStatuses)
		r.Post("/api/agent/container-metrics", s.handleContainerMetrics)
		r.Post("/api/agent/node-metrics", s.handleNodeMetrics)
		r.Post("/api/agent/health-report", s.handleAgentHealthReport)
		r.Post("/api/deployments/{id}/logs", s.appendDeploymentLog)
	})

	return r
}

// ─── Auth Handlers ───────────────────────────────────────────────────────────

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.auth.ListProviders())
}

func (s *Server) handleRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{
		"dataset_nfs_base_path": s.datasetNFSBasePath,
		"base_domain":           s.baseDomain,
	})
}

func (s *Server) handleProviderLogin(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	state := uuid.New().String()
	authURL, err := s.auth.AuthCodeURL(providerName, state)
	if err != nil {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	// Encode provider into the state cookie name so the callback knows which provider to use.
	http.SetCookie(w, &http.Cookie{
		Name: "oauth_state", Value: providerName + ":" + state,
		MaxAge: 300, HttpOnly: true, Path: "/",
		SameSite: http.SameSiteLaxMode, Secure: true,
	})
	s.oauthPending.Store(state, providerName)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleCLILogin initiates the device-flow OAuth for muveectl.
// The CLI passes ?port=PORT and an optional ?provider=NAME.
// Defaults to the first configured provider when provider is omitted.
func (s *Server) handleCLILogin(w http.ResponseWriter, r *http.Request) {
	port := r.URL.Query().Get("port")
	if port == "" {
		http.Error(w, "port required", http.StatusBadRequest)
		return
	}
	providerName := r.URL.Query().Get("provider")
	if providerName == "" {
		providerName = s.auth.DefaultProvider()
	}
	state := uuid.New().String()
	authURL, err := s.auth.AuthCodeURL(providerName, state)
	if err != nil {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	s.cliPending.Store(state, port)
	http.SetCookie(w, &http.Cookie{
		Name: "oauth_state", Value: providerName + ":" + state,
		MaxAge: 300, HttpOnly: true, Path: "/",
		SameSite: http.SameSiteLaxMode, Secure: true,
	})
	s.oauthPending.Store(state, providerName)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleProviderCallback(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	queryState := r.URL.Query().Get("state")

	var state string
	if cookie, err := r.Cookie("oauth_state"); err == nil {
		// Cookie value format: "{provider}:{state}"
		cookieParts := strings.SplitN(cookie.Value, ":", 2)
		if len(cookieParts) == 2 && cookieParts[0] == providerName && cookieParts[1] == queryState {
			state = cookieParts[1]
		}
	}
	// Fallback: verify state against server-side store when cookie is missing (e.g. incognito)
	if state == "" {
		if prov, ok := s.oauthPending.Load(queryState); ok && prov.(string) == providerName {
			state = queryState
		}
	}
	if state == "" {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	s.oauthPending.Delete(state)

	code := r.URL.Query().Get("code")
	if code == "" {
		code = r.URL.Query().Get("authCode") // DingTalk uses authCode instead of code
	}
	user, jwtToken, err := s.auth.HandleCallback(r.Context(), providerName, code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// Check if this was initiated by the CLI device-flow
	if portVal, ok := s.cliPending.LoadAndDelete(state); ok {
		port := portVal.(string)
		apiToken, err := s.auth.CreateAPIToken(r.Context(), user.ID, nil, "CLI Token")
		if err != nil {
			http.Error(w, "failed to create token", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%s?token=%s", port, apiToken.Token), http.StatusFound)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: "muvee_session", Value: jwtToken,
		MaxAge: 7 * 24 * 3600, HttpOnly: true, Path: "/", SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "muvee_session", MaxAge: -1, Path: "/"})
	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	jsonOK(w, user)
}

// handleSkill serves a Markdown skill document that teaches Claude how to use muveectl.
// The server URL is derived from the BASE_DOMAIN environment variable so the example
// is always correct regardless of proxy headers.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	serverURL := "https://" + s.baseDomain
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	fmt.Fprint(w, strings.ReplaceAll(muveectlSkill, "YOUR_SERVER_URL", serverURL))
}

const muveectlSkill = `---
name: muveectl
description: Operate the Muvee self-hosted PaaS via the muveectl CLI. Manages projects (create, update, deploy, delete), datasets (create, scan, delete), and API tokens. Use when the user wants to interact with their Muvee server from the command line, trigger deployments, or manage infrastructure resources.
---

# muveectl – Muvee CLI

## Installation

Download from [GitHub Releases](https://github.com/hoveychen/muvee/releases/latest):

` + "```" + `bash
# macOS (Apple Silicon)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/

# macOS (Intel)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/

# Linux (amd64)
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_linux_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
` + "```" + `

` + "```" + `powershell
# Windows (PowerShell)
Invoke-WebRequest -Uri https://github.com/hoveychen/muvee/releases/latest/download/muveectl_windows_amd64.exe -OutFile muveectl.exe
` + "```" + `

## Authentication

` + "```" + `bash
muveectl login --server YOUR_SERVER_URL   # opens browser for Google OAuth
muveectl whoami
` + "```" + `

Config saved at ` + "`~/.config/muveectl/config.json`" + `.

## Projects

` + "```" + `bash
muveectl projects list
muveectl projects create --name NAME --git-url URL \
  [--branch BRANCH] [--domain PREFIX] [--dockerfile PATH] \
  [--auth-required] [--auth-domains example.com,corp.com]
muveectl projects create --name NAME --git-source hosted \
  [--domain PREFIX] [--dockerfile PATH]
muveectl projects get PROJECT_ID
muveectl projects update PROJECT_ID [--branch BRANCH] [--auth-required] [--no-auth] [--auth-domains DOMAINS]
muveectl projects deploy PROJECT_ID
muveectl projects deployments PROJECT_ID
muveectl projects delete PROJECT_ID
` + "```" + `

### Google OAuth protection (` + "`--auth-required`" + `)

When enabled, Traefik intercepts every request and redirects unauthenticated users to Google OAuth login before forwarding to the container.

- ` + "`--auth-required`" + ` — enable protection
- ` + "`--no-auth`" + ` — disable protection
- ` + "`--auth-domains example.com,corp.com`" + ` — restrict to specific email domains (optional)

The authenticated user's email is forwarded to the container via the **` + "`X-Forwarded-User`" + `** HTTP header:

` + "```" + `python
# Python / Flask
user_email = request.headers.get("X-Forwarded-User")  # e.g. "alice@example.com"
` + "```" + `

` + "```" + `go
// Go
userEmail := r.Header.Get("X-Forwarded-User")
` + "```" + `

` + "```" + `typescript
// Node.js / Express
const userEmail = req.headers["x-forwarded-user"]
` + "```" + `

## Datasets

` + "```" + `bash
muveectl datasets list
muveectl datasets create --name NAME --nfs-path NFS_PATH
muveectl datasets get DATASET_ID
muveectl datasets scan DATASET_ID
muveectl datasets delete DATASET_ID
` + "```" + `

## API Tokens (project-scoped)

Tokens are scoped to a single project. Use them for git push authentication and project-level CLI access.

` + "```" + `bash
muveectl tokens PROJECT_ID list
muveectl tokens PROJECT_ID create [--name NAME]   # token value shown once on creation
muveectl tokens PROJECT_ID delete TOKEN_ID
` + "```" + `

## Secrets

` + "```" + `bash
muveectl secrets list
muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519
muveectl secrets delete SECRET_ID
` + "```" + `

### Project Secret Bindings

` + "```" + `bash
# Runtime env var
muveectl projects bind-secret PROJECT_ID --secret-id SECRET_ID --env-var GITHUB_TOKEN

# Git clone auth
muveectl projects bind-secret PROJECT_ID --secret-id SECRET_ID --use-for-git --git-username x-access-token

# Build-time secret
muveectl projects bind-secret PROJECT_ID --secret-id SECRET_ID --use-for-build --build-secret-id github_token
` + "```" + `

## Global Flags

| Flag | Description |
|------|-------------|
| ` + "`--server URL`" + ` | Override the configured server URL for this call |
| ` + "`--json`" + `      | Output raw JSON (pipe-friendly) |

## Git Repository Requirements

For a project to deploy successfully the repository must satisfy:

### Build
- Accessible via ` + "`git clone --depth=1`" + ` over HTTPS (public or token secret) or SSH (SSH key secret)
- The configured branch must exist (default: ` + "`main`" + `)
- A ` + "`Dockerfile`" + ` must exist at the configured path (default: ` + "`Dockerfile`" + ` in repo root)
- Image must build for **` + "`linux/amd64`" + `** (` + "`docker buildx build --platform linux/amd64`" + `)
- For private dependencies during build, bind a secret with ` + "`--use-for-build --build-secret-id <id>`" + ` and read ` + "`/run/secrets/<id>`" + ` in Dockerfile

### Runtime
- Container must serve **HTTP** on port **8080** — Traefik handles TLS termination
- Do not start HTTPS inside the container
- App will be reachable at ` + "`https://<domain_prefix>.<base_domain>`" + `

### Dataset mounts
Datasets are injected as Docker volumes at ` + "`/data/<dataset_name>`" + ` inside the container:

| Mode | Access |
|------|--------|
| ` + "`dependency`" + ` | Read-only — rsync-cached local copy |
| ` + "`readwrite`" + `  | Read-write — direct NFS mount |

## Hosted Git Repositories

When creating a project with ` + "`--git-source hosted`" + `, Muvee creates a bare git repository on the server. After creation, you receive a push URL. Push your code using any git username and your Muvee API token as the password:

` + "```" + `bash
muveectl projects create --name my-app --git-source hosted
# → Created project my-app (ID: abc-123)
# → Git Push URL: https://muvee.example.com/git/abc-123.git

git remote add muvee https://muvee.example.com/git/abc-123.git
git push muvee main
# When prompted: username = anything, password = your mvt_* project token
# Create a token first: muveectl tokens PROJECT_ID create --name "git push"
` + "```" + `

### Repository Browser

` + "```" + `bash
muveectl projects repo PROJECT_ID tree [--ref main] [--path src/]
muveectl projects repo PROJECT_ID log [--ref main] [--limit 20]
muveectl projects repo PROJECT_ID branches
muveectl projects repo PROJECT_ID show main:README.md
` + "```" + `

## Typical Workflow

1. Get project IDs: ` + "`muveectl projects list --json`" + `
2. Deploy a project: ` + "`muveectl projects deploy PROJECT_ID`" + `
3. Check status: ` + "`muveectl projects deployments PROJECT_ID`" + `
`

// ─── API Tokens ──────────────────────────────────────────────────────────────

func (s *Server) listProjectTokens(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	tokens, err := s.store.ListAPITokensForProject(r.Context(), projectID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	// Mask token_hash before sending to client
	type safeToken struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		LastUsedAt *string `json:"last_used_at"`
		CreatedAt  string  `json:"created_at"`
	}
	out := make([]safeToken, 0, len(tokens))
	for _, t := range tokens {
		st := safeToken{
			ID:        t.ID.String(),
			Name:      t.Name,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if t.LastUsedAt != nil {
			s := t.LastUsedAt.Format(time.RFC3339)
			st.LastUsedAt = &s
		}
		out = append(out, st)
	}
	jsonOK(w, out)
}

func (s *Server) createProjectToken(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if body.Name == "" {
		body.Name = "Git Token"
	}
	token, err := s.auth.CreateAPIToken(r.Context(), user.ID, &projectID, body.Name)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	// Return the raw token value only on creation
	jsonOK(w, map[string]string{
		"id":    token.ID.String(),
		"name":  token.Name,
		"token": token.Token,
	})
}

func (s *Server) deleteProjectToken(w http.ResponseWriter, r *http.Request) {
	tokenID := mustParseUUID(chi.URLParam(r, "tokenId"))
	user := auth.UserFromCtx(r.Context())
	if err := s.store.DeleteAPIToken(r.Context(), tokenID, user.ID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Projects ────────────────────────────────────────────────────────────────

// reservedDomainPrefixes are subdomain prefixes occupied by system services.
// User-created projects must not use these names to avoid routing conflicts.
var reservedDomainPrefixes = map[string]bool{
	"registry": true,
	"traefik":  true,
	"muvee":    true,
}

// validDomainPrefix matches RFC-1123 subdomain labels: lowercase alphanumeric
// and hyphens, must start and end with an alphanumeric character.
var validDomainPrefix = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func validateDomainPrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("domain prefix must not be empty")
	}
	if !validDomainPrefix.MatchString(prefix) {
		return fmt.Errorf("domain prefix %q is invalid: only lowercase letters, digits, and hyphens are allowed, and must start/end with a letter or digit", prefix)
	}
	if reservedDomainPrefixes[prefix] {
		return fmt.Errorf("domain prefix %q is reserved", prefix)
	}
	return nil
}

// validateProject checks required fields and resolves DomainPrefix.
// If DomainPrefix is not set, Name is used as the default; if Name is not a
// valid subdomain label either, an explicit DomainPrefix is required.
func validateProject(p *store.Project) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("project name must not be empty")
	}
	if p.GitSource == "" {
		p.GitSource = store.GitSourceExternal
	}
	if p.GitSource != store.GitSourceExternal && p.GitSource != store.GitSourceHosted {
		return fmt.Errorf("git_source must be 'external' or 'hosted'")
	}
	if p.DomainPrefix == "" {
		if err := validateDomainPrefix(p.Name); err != nil {
			return fmt.Errorf("domain_prefix is required because project name %q cannot be used as a subdomain: %w", p.Name, err)
		}
		p.DomainPrefix = p.Name
		return nil
	}
	return validateDomainPrefix(p.DomainPrefix)
}

// handlePublicProjects returns all currently running projects with owner info.
// This endpoint requires no authentication and is used by the community homepage.
// Each project URL is computed from s.baseDomain so the frontend can build links.
func (s *Server) handlePublicProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListPublicRunningProjects(r.Context())
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	type publicProjectOut struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		DomainPrefix   string `json:"domain_prefix"`
		URL            string `json:"url"`
		AuthRequired   bool   `json:"auth_required"`
		OwnerName      string `json:"owner_name"`
		OwnerAvatarURL string `json:"owner_avatar_url"`
		UpdatedAt      int64  `json:"updated_at"`
	}
	out := make([]publicProjectOut, 0, len(projects))
	for _, p := range projects {
		out = append(out, publicProjectOut{
			ID:             p.ID.String(),
			Name:           p.Name,
			DomainPrefix:   p.DomainPrefix,
			URL:            fmt.Sprintf("https://%s.%s", p.DomainPrefix, s.baseDomain),
			AuthRequired:   p.AuthRequired,
			OwnerName:      p.OwnerName,
			OwnerAvatarURL: p.OwnerAvatarURL,
			UpdatedAt:      p.UpdatedAt.Unix(),
		})
	}
	jsonOK(w, out)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	projects, err := s.store.ListProjectsForUser(r.Context(), user.ID, user.Role == store.UserRoleAdmin)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	for _, p := range projects {
		if p.GitSource == store.GitSourceHosted {
			p.GitPushURL = s.hostedGitPushURL(p.ID)
		}
	}
	jsonOK(w, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	var p store.Project
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := validateProject(&p); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if existing, err := s.store.GetProjectByDomainPrefix(r.Context(), p.DomainPrefix); err != nil {
		jsonErr(w, err, 500)
		return
	} else if existing != nil {
		jsonErr(w, fmt.Errorf("domain prefix %q is already in use by another project", p.DomainPrefix), 409)
		return
	}

	// For hosted repos: initialize a bare git repo and set the sentinel git_url.
	if p.GitSource == store.GitSourceHosted {
		if s.gitRepoBasePath == "" {
			jsonErr(w, fmt.Errorf("hosted git repositories are not enabled on this server (GIT_REPO_BASE_PATH not set)"), 400)
			return
		}
	}

	p.OwnerID = user.ID
	created, err := s.store.CreateProject(r.Context(), &p)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	if created.GitSource == store.GitSourceHosted {
		repoPath := gitrepo.RepoPath(s.gitRepoBasePath, created.ID)
		if err := gitrepo.InitBareRepo(repoPath); err != nil {
			// Clean up the project if repo init fails.
			_ = s.store.DeleteProject(r.Context(), created.ID)
			jsonErr(w, fmt.Errorf("failed to initialize git repository: %w", err), 500)
			return
		}
		created.GitURL = "hosted://" + created.ID.String()
		_ = s.store.UpdateProject(r.Context(), created)
		created.GitPushURL = s.hostedGitPushURL(created.ID)
	}

	jsonOK(w, created)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 404)
		return
	}
	p, err := s.store.GetProject(r.Context(), id)
	if err != nil || p == nil {
		jsonErr(w, err, 404)
		return
	}
	if p.GitSource == store.GitSourceHosted {
		p.GitPushURL = s.hostedGitPushURL(p.ID)
	}
	jsonOK(w, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	var p store.Project
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := validateProject(&p); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if existing, err := s.store.GetProjectByDomainPrefix(r.Context(), p.DomainPrefix); err != nil {
		jsonErr(w, err, 500)
		return
	} else if existing != nil && existing.ID != id {
		jsonErr(w, fmt.Errorf("domain prefix %q is already in use by another project", p.DomainPrefix), 409)
		return
	}
	p.ID = id
	if err := s.store.UpdateProject(r.Context(), &p); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	// Clean up hosted git repo if applicable.
	if s.gitRepoBasePath != "" {
		if p, err := s.store.GetProject(r.Context(), id); err == nil && p != nil && p.GitSource == store.GitSourceHosted {
			_ = gitrepo.DeleteRepo(gitrepo.RepoPath(s.gitRepoBasePath, id))
		}
	}
	jsonOK(w, map[string]string{"status": "ok"})
	_ = s.store.DeleteProject(r.Context(), id)
}

func (s *Server) getProjectDatasets(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	pds, err := s.store.GetProjectDatasets(r.Context(), id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, pds)
}

func (s *Server) setProjectDatasets(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	var items []store.ProjectDataset
	if err := json.NewDecoder(r.Body).Decode(&items); err != nil {
		jsonErr(w, err, 400)
		return
	}
	// Verify user can access each dataset
	for i := range items {
		items[i].ProjectID = id
		dsOk, _ := s.store.CanAccessDataset(r.Context(), user.ID, items[i].DatasetID, user.Role == store.UserRoleAdmin)
		if !dsOk {
			jsonErr(w, nil, 403)
			return
		}
	}
	if err := s.store.SetProjectDatasets(r.Context(), id, items); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, items)
}

func (s *Server) triggerDeploy(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	project, err := s.store.GetProject(r.Context(), id)
	if err != nil || project == nil {
		jsonErr(w, err, 404)
		return
	}
	deployment, err := s.store.CreateDeployment(r.Context(), &store.Deployment{ProjectID: id})
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if err := s.sched.DispatchBuild(r.Context(), deployment, project); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, deployment)
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	deployments, err := s.store.ListDeployments(r.Context(), id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, deployments)
}

// ─── Datasets ────────────────────────────────────────────────────────────────

func (s *Server) listDatasets(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	datasets, err := s.store.ListDatasetsForUser(r.Context(), user.ID, user.Role == store.UserRoleAdmin)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, datasets)
}

func (s *Server) createDataset(w http.ResponseWriter, r *http.Request) {
	if s.datasetNFSBasePath == "" {
		jsonErr(w, fmt.Errorf("DATASET_NFS_BASE_PATH is not configured"), 503)
		return
	}
	user := auth.UserFromCtx(r.Context())
	var d store.Dataset
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonErr(w, err, 400)
		return
	}
	subPath, err := validateDatasetSubPath(d.NFSPath)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	d.NFSPath = subPath
	d.OwnerID = user.ID
	created, err := s.store.CreateDataset(r.Context(), &d)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, created)
}

func (s *Server) getDataset(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessDataset(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 404)
		return
	}
	d, err := s.store.GetDataset(r.Context(), id)
	if err != nil || d == nil {
		jsonErr(w, err, 404)
		return
	}
	jsonOK(w, d)
}

func (s *Server) updateDataset(w http.ResponseWriter, r *http.Request) {
	if s.datasetNFSBasePath == "" {
		jsonErr(w, fmt.Errorf("DATASET_NFS_BASE_PATH is not configured"), 503)
		return
	}
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessDataset(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	var d store.Dataset
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonErr(w, err, 400)
		return
	}
	subPath, err := validateDatasetSubPath(d.NFSPath)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	d.NFSPath = subPath
	d.ID = id
	if err := s.store.UpdateDataset(r.Context(), &d); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, d)
}

func validateDatasetSubPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("nfs_path is required")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("nfs_path must be a relative sub-path")
	}
	clean := filepath.Clean(p)
	if clean == "." {
		return "", fmt.Errorf("nfs_path is required")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("nfs_path must be a relative sub-path")
	}
	return clean, nil
}

func (s *Server) deleteDataset(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessDataset(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	_ = s.store.DeleteDataset(r.Context(), id)
	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) scanDataset(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessDataset(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	ds, err := s.store.GetDataset(r.Context(), id)
	if err != nil || ds == nil {
		jsonErr(w, err, 404)
		return
	}
	go func() {
		_ = s.monitor.ScanDataset(r.Context(), ds)
	}()
	jsonOK(w, map[string]string{"status": "scanning"})
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	snaps, err := s.store.ListSnapshotsForDataset(r.Context(), id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, snaps)
}

func (s *Server) listFileHistory(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	filePath := r.URL.Query().Get("file")
	history, err := s.store.ListFileHistory(r.Context(), id, filePath, 500)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, history)
}

// ─── Nodes & Users ───────────────────────────────────────────────────────────

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.ListNodes(r.Context())
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, nodes)
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	if err := s.store.DeleteNode(r.Context(), id); err != nil {
		jsonErr(w, err, 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getNodeMetrics returns the latest host-level metric sample for a node.
func (s *Server) getNodeMetrics(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	m, err := s.store.GetLatestNodeMetricByNodeID(r.Context(), id)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if m == nil {
		jsonOK(w, nil)
		return
	}
	jsonOK(w, map[string]interface{}{
		"node_id":          m.NodeID,
		"collected_at":     m.CollectedAt.Unix(),
		"cpu_percent":      m.CPUPercent,
		"mem_total_bytes":  m.MemTotalBytes,
		"mem_used_bytes":   m.MemUsedBytes,
		"disk_total_bytes": m.DiskTotalBytes,
		"disk_used_bytes":  m.DiskUsedBytes,
		"load1":            m.Load1,
		"load5":            m.Load5,
		"load15":           m.Load15,
	})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, users)
}

func (s *Server) setUserRole(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	var body struct{ Role string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := s.store.SetUserRole(r.Context(), id, store.UserRole(body.Role)); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Agent Endpoints ─────────────────────────────────────────────────────────

// handleAgentConfig returns runtime configuration that agents should use instead of
// reading from local environment variables. This ensures all agents stay consistent
// with the control plane's own configuration (registry credentials, base domain, etc.).
func (s *Server) handleAgentConfig(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]string{
		"registry_addr":         s.registryAddr,
		"registry_user":         s.registryUser,
		"registry_password":     s.registryPassword,
		"base_domain":           s.baseDomain,
		"volume_nfs_base_path":  s.volumeNFSBasePath,
		"dataset_nfs_base_path": s.datasetNFSBasePath,
	})
}

// handleContainerStatuses receives a batch of container runtime statuses from deploy agents
// and updates the restart_count / oom_killed fields on the currently running deployment
// for each reported container (identified by domain_prefix).
func (s *Server) handleContainerStatuses(w http.ResponseWriter, r *http.Request) {
	var statuses []struct {
		DomainPrefix string `json:"domain_prefix"`
		RestartCount int    `json:"restart_count"`
		OOMKilled    bool   `json:"oom_killed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&statuses); err != nil {
		jsonErr(w, err, 400)
		return
	}
	for _, s2 := range statuses {
		_ = s.store.UpdateDeploymentRuntimeStatus(r.Context(), s2.DomainPrefix, s2.RestartCount, s2.OOMKilled)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// handleContainerMetrics receives a batch of resource metric samples from deploy agents
// and persists them in the container_metrics table.
func (s *Server) handleContainerMetrics(w http.ResponseWriter, r *http.Request) {
	var reports []struct {
		DomainPrefix    string  `json:"domain_prefix"`
		CPUPercent      float64 `json:"cpu_percent"`
		MemUsageBytes   int64   `json:"mem_usage_bytes"`
		MemLimitBytes   int64   `json:"mem_limit_bytes"`
		NetRxBytes      int64   `json:"net_rx_bytes"`
		NetTxBytes      int64   `json:"net_tx_bytes"`
		BlockReadBytes  int64   `json:"block_read_bytes"`
		BlockWriteBytes int64   `json:"block_write_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reports); err != nil {
		jsonErr(w, err, 400)
		return
	}
	for _, rep := range reports {
		m := &store.ContainerMetric{
			CPUPercent:      rep.CPUPercent,
			MemUsageBytes:   rep.MemUsageBytes,
			MemLimitBytes:   rep.MemLimitBytes,
			NetRxBytes:      rep.NetRxBytes,
			NetTxBytes:      rep.NetTxBytes,
			BlockReadBytes:  rep.BlockReadBytes,
			BlockWriteBytes: rep.BlockWriteBytes,
		}
		_ = s.store.InsertContainerMetricByDomainPrefix(r.Context(), rep.DomainPrefix, m)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// handleNodeMetrics receives a host-level resource metric sample from an agent and persists it.
func (s *Server) handleNodeMetrics(w http.ResponseWriter, r *http.Request) {
	var rep struct {
		NodeID         string  `json:"node_id"`
		CPUPercent     float64 `json:"cpu_percent"`
		MemTotalBytes  int64   `json:"mem_total_bytes"`
		MemUsedBytes   int64   `json:"mem_used_bytes"`
		DiskTotalBytes int64   `json:"disk_total_bytes"`
		DiskUsedBytes  int64   `json:"disk_used_bytes"`
		Load1          float64 `json:"load1"`
		Load5          float64 `json:"load5"`
		Load15         float64 `json:"load15"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		jsonErr(w, err, 400)
		return
	}
	nodeID, err := uuid.Parse(rep.NodeID)
	if err != nil {
		jsonErr(w, fmt.Errorf("invalid node_id"), 400)
		return
	}
	m := &store.NodeMetric{
		NodeID:         nodeID,
		CPUPercent:     rep.CPUPercent,
		MemTotalBytes:  rep.MemTotalBytes,
		MemUsedBytes:   rep.MemUsedBytes,
		DiskTotalBytes: rep.DiskTotalBytes,
		DiskUsedBytes:  rep.DiskUsedBytes,
		Load1:          rep.Load1,
		Load5:          rep.Load5,
		Load15:         rep.Load15,
	}
	_ = s.store.InsertNodeMetric(r.Context(), m)
	jsonOK(w, map[string]string{"status": "ok"})
}

// getProjectMetrics returns recent container metric samples for a project's running deployment.
// Query param: limit (default 60, max 1440).
func (s *Server) getProjectMetrics(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	limit := 60
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil {
			if limit > 1440 {
				limit = 1440
			}
			if limit < 1 {
				limit = 1
			}
		}
	}
	metrics, err := s.store.GetContainerMetricsForProject(r.Context(), id, limit)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	// Return epoch timestamps for frontend compatibility.
	type metricOut struct {
		DeploymentID    string  `json:"deployment_id"`
		CollectedAt     int64   `json:"collected_at"`
		CPUPercent      float64 `json:"cpu_percent"`
		MemUsageBytes   int64   `json:"mem_usage_bytes"`
		MemLimitBytes   int64   `json:"mem_limit_bytes"`
		NetRxBytes      int64   `json:"net_rx_bytes"`
		NetTxBytes      int64   `json:"net_tx_bytes"`
		BlockReadBytes  int64   `json:"block_read_bytes"`
		BlockWriteBytes int64   `json:"block_write_bytes"`
	}
	out := make([]metricOut, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, metricOut{
			DeploymentID:    m.DeploymentID.String(),
			CollectedAt:     m.CollectedAt.Unix(),
			CPUPercent:      m.CPUPercent,
			MemUsageBytes:   m.MemUsageBytes,
			MemLimitBytes:   m.MemLimitBytes,
			NetRxBytes:      m.NetRxBytes,
			NetTxBytes:      m.NetTxBytes,
			BlockReadBytes:  m.BlockReadBytes,
			BlockWriteBytes: m.BlockWriteBytes,
		})
	}
	jsonOK(w, out)
}

func (s *Server) registerNode(w http.ResponseWriter, r *http.Request) {
	var n store.Node
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		jsonErr(w, err, 400)
		return
	}
	registered, err := s.store.UpsertNode(r.Context(), &n)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, registered)
}

func (s *Server) pollTasks(w http.ResponseWriter, r *http.Request) {
	nodeIDStr := r.URL.Query().Get("node_id")
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	// Update heartbeat so the node stays online.
	if err := s.store.TouchNode(r.Context(), nodeID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	tasks, err := s.store.PollTasksForNode(r.Context(), nodeID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, tasks)
}

func (s *Server) completeTask(w http.ResponseWriter, r *http.Request) {
	taskID := mustParseUUID(chi.URLParam(r, "id"))
	var body struct {
		Status   string `json:"status"`
		Result   string `json:"result"`
		ImageTag string `json:"image_tag"` // build tasks: triggers deploy dispatch
		HostPort int    `json:"host_port"` // deploy tasks: port the container listens on
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	status := store.TaskStatus(body.Status)
	result := body.Result
	if body.ImageTag != "" {
		if b, err := json.Marshal(map[string]string{"image_tag": body.ImageTag}); err == nil {
			result = string(b)
		}
	}
	if err := s.store.UpdateTaskStatus(r.Context(), taskID, status, result); err != nil {
		jsonErr(w, err, 500)
		return
	}

	if status == store.TaskStatusRunning {
		task, err := s.store.GetTask(r.Context(), taskID)
		if err == nil && task != nil && task.Type == store.TaskTypeBuild {
			_ = s.store.UpdateDeploymentStatus(r.Context(), task.DeploymentID, store.DeploymentStatusBuilding, "")
		}
		jsonOK(w, map[string]string{"status": "ok"})
		return
	}

	if status == store.TaskStatusFailed {
		task, err := s.store.GetTask(r.Context(), taskID)
		if err == nil && task != nil {
			errMsg := body.Result
			if errMsg == "" {
				errMsg = "task failed"
			}
			_ = s.store.UpdateDeploymentStatus(r.Context(), task.DeploymentID, store.DeploymentStatusFailed, errMsg)
		}
		jsonOK(w, map[string]string{"status": "ok"})
		return
	}

	if status != store.TaskStatusCompleted {
		jsonOK(w, map[string]string{"status": "ok"})
		return
	}

	task, err := s.store.GetTask(r.Context(), taskID)
	if err != nil || task == nil {
		jsonOK(w, map[string]string{"status": "ok"})
		return
	}

	switch task.Type {
	case store.TaskTypeDeploy:
		if body.HostPort > 0 {
			_ = s.store.SetDeploymentHostPort(r.Context(), task.DeploymentID, body.HostPort)
			// Retire previous running deployments for the same project.
			// For any retired deployment that ran on a different node, dispatch a cleanup
			// task so the stale container is removed from that node.
			if dep, err := s.store.GetDeployment(r.Context(), task.DeploymentID); err == nil && dep != nil {
				if project, err := s.store.GetProject(r.Context(), dep.ProjectID); err == nil && project != nil {
					if stopped, err := s.store.StopProjectDeployments(r.Context(), dep.ProjectID, task.DeploymentID); err == nil {
						for _, old := range stopped {
							if old.NodeID != nil && task.NodeID != nil && *old.NodeID != *task.NodeID {
								_ = s.sched.DispatchCleanup(r.Context(), *old.NodeID, old, project.DomainPrefix)
							}
						}
					}
				}
			}
		} else {
			_ = s.store.UpdateDeploymentStatus(r.Context(), task.DeploymentID, store.DeploymentStatusFailed, "deploy completed but no host_port reported")
		}
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) appendDeploymentLog(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	var body struct {
		Line string `json:"line"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := s.store.AppendDeploymentLog(r.Context(), id, body.Line); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Traefik HTTP Provider ────────────────────────────────────────────────────

// traefikDynamicConfig is the shape Traefik expects from an HTTP provider endpoint.
type traefikDynamicConfig struct {
	HTTP traefikHTTP `json:"http"`
}

type traefikHTTP struct {
	Routers     map[string]traefikRouter     `json:"routers"`
	Services    map[string]traefikService    `json:"services"`
	Middlewares map[string]traefikMiddleware `json:"middlewares,omitempty"`
}

type traefikRouter struct {
	Rule        string      `json:"rule"`
	EntryPoints []string    `json:"entryPoints"`
	Service     string      `json:"service"`
	Middlewares []string    `json:"middlewares,omitempty"`
	TLS         *traefikTLS `json:"tls,omitempty"`
}

type traefikTLS struct {
	CertResolver string `json:"certResolver,omitempty"`
}

type traefikService struct {
	LoadBalancer traefikLB `json:"loadBalancer"`
}

type traefikLB struct {
	Servers []traefikServer `json:"servers"`
}

type traefikServer struct {
	URL string `json:"url"`
}

type traefikMiddleware struct {
	ForwardAuth *traefikForwardAuth `json:"forwardAuth,omitempty"`
}

type traefikForwardAuth struct {
	Address             string   `json:"address"`
	AuthResponseHeaders []string `json:"authResponseHeaders"`
	TrustForwardHeader  bool     `json:"trustForwardHeader"`
}

// handleTraefikConfig generates a Traefik dynamic configuration for all running deployments.
// Traefik polls this endpoint via its HTTP provider.
func (s *Server) handleTraefikConfig(w http.ResponseWriter, r *http.Request) {
	deployments, err := s.store.GetRunningDeployments(r.Context())
	if err != nil {
		jsonErr(w, err, 500)
		return
	}

	cfg := traefikDynamicConfig{
		HTTP: traefikHTTP{
			Routers:  make(map[string]traefikRouter),
			Services: make(map[string]traefikService),
		},
	}

	for _, dep := range deployments {
		name := dep.DomainPrefix
		host := fmt.Sprintf("%s.%s", dep.DomainPrefix, s.baseDomain)
		backendURL := fmt.Sprintf("http://%s:%d", dep.HostIP, dep.HostPort)

		// HTTPS router. The web (port 80) entrypoint in traefik.yml already has a
		// global HTTP→HTTPS redirect, so we only need the HTTPS router here.
		// Generating a separate HTTP router that references redirect-to-https@file
		// (a cross-provider middleware) can cause Traefik to reject the entire HTTP
		// provider config when the cross-provider reference can't be resolved.
		httpsRouter := traefikRouter{
			Rule:        fmt.Sprintf("Host(`%s`)", host),
			EntryPoints: []string{"websecure"},
			Service:     name,
			TLS:         &traefikTLS{CertResolver: "letsencrypt"},
		}

		// Per-project ForwardAuth middleware (if auth is required)
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
					AuthResponseHeaders: []string{"X-Forwarded-User"},
					TrustForwardHeader:  true,
				},
			}
			httpsRouter.Middlewares = []string{mwName}
		}

		cfg.HTTP.Routers[name] = httpsRouter
		cfg.HTTP.Services[name] = traefikService{
			LoadBalancer: traefikLB{
				Servers: []traefikServer{{URL: backendURL}},
			},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := http.StatusText(code)
	if err != nil {
		msg = err.Error()
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func mustParseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

// Suppress unused import
var _ = time.Now

// ─── Secrets ─────────────────────────────────────────────────────────────────

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	secrets, err := s.store.ListSecretsForUser(r.Context(), user.ID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	type safeSecret struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	out := make([]safeSecret, 0, len(secrets))
	for _, sec := range secrets {
		out = append(out, safeSecret{
			ID:        sec.ID.String(),
			Name:      sec.Name,
			Type:      string(sec.Type),
			CreatedAt: sec.CreatedAt.Format(time.RFC3339),
			UpdatedAt: sec.UpdatedAt.Format(time.RFC3339),
		})
	}
	jsonOK(w, out)
}

func (s *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	var body struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if body.Name == "" || body.Value == "" {
		jsonErr(w, fmt.Errorf("name and value are required"), 400)
		return
	}
	if body.Type != "password" && body.Type != "ssh_key" {
		jsonErr(w, fmt.Errorf("type must be 'password' or 'ssh_key'"), 400)
		return
	}
	sec, err := s.store.CreateSecret(r.Context(), user.ID, body.Name, store.SecretType(body.Type), body.Value)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{
		"id":   sec.ID.String(),
		"name": sec.Name,
		"type": string(sec.Type),
	})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	if err := s.store.DeleteSecret(r.Context(), id, user.ID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) getProjectSecrets(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	bindings, err := s.store.GetProjectSecretsWithMeta(r.Context(), projectID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	type item struct {
		SecretID      string `json:"secret_id"`
		SecretName    string `json:"secret_name"`
		SecretType    string `json:"secret_type"`
		EnvVarName    string `json:"env_var_name"`
		UseForGit     bool   `json:"use_for_git"`
		UseForBuild   bool   `json:"use_for_build"`
		BuildSecretID string `json:"build_secret_id"`
		GitUsername   string `json:"git_username"`
	}
	out := make([]item, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, item{
			SecretID:      b.SecretID.String(),
			SecretName:    b.SecretName,
			SecretType:    string(b.SecretType),
			EnvVarName:    b.EnvVarName,
			UseForGit:     b.UseForGit,
			UseForBuild:   b.UseForBuild,
			BuildSecretID: b.BuildSecretID,
			GitUsername:   b.GitUsername,
		})
	}
	jsonOK(w, out)
}

func (s *Server) setProjectSecrets(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	var body []struct {
		SecretID      string `json:"secret_id"`
		EnvVarName    string `json:"env_var_name"`
		UseForGit     bool   `json:"use_for_git"`
		UseForBuild   bool   `json:"use_for_build"`
		BuildSecretID string `json:"build_secret_id"`
		GitUsername   string `json:"git_username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	var bindings []store.ProjectSecret
	for _, b := range body {
		bindings = append(bindings, store.ProjectSecret{
			ProjectID:     projectID,
			SecretID:      mustParseUUID(b.SecretID),
			EnvVarName:    b.EnvVarName,
			UseForGit:     b.UseForGit,
			UseForBuild:   b.UseForBuild,
			BuildSecretID: strings.TrimSpace(b.BuildSecretID),
			GitUsername:   b.GitUsername,
		})
	}
	if err := s.store.SetProjectSecrets(r.Context(), projectID, bindings); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Workspace File Management ────────────────────────────────────────────────

// workspaceDir returns the host-side NFS directory for a project's volume.
// Returns an error if VOLUME_NFS_BASE_PATH is not configured.
func (s *Server) workspaceDir(projectID uuid.UUID) (string, error) {
	if s.volumeNFSBasePath == "" {
		return "", fmt.Errorf("VOLUME_NFS_BASE_PATH is not configured on this server")
	}
	return filepath.Join(s.volumeNFSBasePath, projectID.String()), nil
}

// workspaceSafePath joins base and subPath while preventing path traversal.
func workspaceSafePath(base, subPath string) (string, error) {
	joined := filepath.Join(base, filepath.Clean("/"+subPath))
	if !strings.HasPrefix(joined, base) {
		return "", fmt.Errorf("path traversal detected")
	}
	return joined, nil
}

type workspaceEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir"`
	ModTime int64  `json:"mod_time"`
}

// workspaceList lists files in a workspace directory.
// GET /api/projects/{id}/workspace?path=<subdir>
func (s *Server) workspaceList(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, projectID, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 404)
		return
	}
	base, err := s.workspaceDir(projectID)
	if err != nil {
		jsonErr(w, err, 503)
		return
	}
	subPath := r.URL.Query().Get("path")
	dir, err := workspaceSafePath(base, subPath)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		jsonOK(w, []workspaceEntry{})
		return
	}
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	result := make([]workspaceEntry, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		var size int64
		var modTime int64
		if info != nil {
			size = info.Size()
			modTime = info.ModTime().Unix()
		}
		result = append(result, workspaceEntry{
			Name:    e.Name(),
			Size:    size,
			IsDir:   e.IsDir(),
			ModTime: modTime,
		})
	}
	jsonOK(w, result)
}

// workspaceDownload streams a single file to the client.
// GET /api/projects/{id}/workspace/download?path=<file>
func (s *Server) workspaceDownload(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, projectID, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 404)
		return
	}
	base, err := s.workspaceDir(projectID)
	if err != nil {
		jsonErr(w, err, 503)
		return
	}
	subPath := r.URL.Query().Get("path")
	if subPath == "" {
		jsonErr(w, fmt.Errorf("path is required"), 400)
		return
	}
	filePath, err := workspaceSafePath(base, subPath)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	f, err := os.Open(filePath)
	if os.IsNotExist(err) {
		jsonErr(w, fmt.Errorf("file not found"), 404)
		return
	}
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	if info != nil && info.IsDir() {
		jsonErr(w, fmt.Errorf("path is a directory"), 400)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(filePath))
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, f)
}

// workspaceUpload saves an uploaded file into the workspace.
// POST /api/projects/{id}/workspace/upload?path=<subdir>
func (s *Server) workspaceUpload(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, projectID, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	base, err := s.workspaceDir(projectID)
	if err != nil {
		jsonErr(w, err, 503)
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		jsonErr(w, err, 400)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	defer file.Close()
	subPath := r.URL.Query().Get("path")
	destDir, err := workspaceSafePath(base, subPath)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		jsonErr(w, err, 500)
		return
	}
	destPath := filepath.Join(destDir, filepath.Base(header.Filename))
	out, err := os.Create(destPath)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok", "path": strings.TrimPrefix(destPath, base)})
}

// workspaceDelete removes a file or directory from the workspace.
// DELETE /api/projects/{id}/workspace?path=<path>
func (s *Server) workspaceDelete(w http.ResponseWriter, r *http.Request) {
	projectID := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, projectID, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 403)
		return
	}
	base, err := s.workspaceDir(projectID)
	if err != nil {
		jsonErr(w, err, 503)
		return
	}
	subPath := r.URL.Query().Get("path")
	if subPath == "" {
		jsonErr(w, fmt.Errorf("path is required"), 400)
		return
	}
	target, err := workspaceSafePath(base, subPath)
	if err != nil {
		jsonErr(w, err, 400)
		return
	}
	if target == base {
		jsonErr(w, fmt.Errorf("cannot delete the workspace root"), 400)
		return
	}
	if err := os.RemoveAll(target); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Hosted Git Repository ──────────────────────────────────────────────────

// hostedGitPushURL computes the user-facing push URL for a hosted project.
func (s *Server) hostedGitPushURL(projectID uuid.UUID) string {
	scheme := "https"
	if s.baseDomain == "localhost" || strings.HasPrefix(s.baseDomain, "localhost:") {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/git/%s.git", scheme, s.baseDomain, projectID)
}

// gitHTTPAuth authenticates a Git Smart HTTP request using HTTP Basic Auth.
// The password is treated as an mvt_* API token (or the agent secret).
func (s *Server) gitHTTPAuth(r *http.Request, projectID uuid.UUID) error {
	_, password, ok := r.BasicAuth()
	if !ok {
		return fmt.Errorf("basic auth required")
	}

	// Allow agent secret for builder cloning.
	if s.agentSecret != "" && password == s.agentSecret {
		return nil
	}

	// Validate API token.
	if !strings.HasPrefix(password, "mvt_") {
		return fmt.Errorf("invalid token")
	}
	hash := sha256.Sum256([]byte(password))
	hashHex := hex.EncodeToString(hash[:])
	token, err := s.store.GetAPITokenByHash(r.Context(), hashHex)
	if err != nil || token == nil {
		return fmt.Errorf("invalid token")
	}

	// If the token is project-scoped, it must match the requested project.
	if token.ProjectID != nil && *token.ProjectID != projectID {
		return fmt.Errorf("token not valid for this project")
	}

	// For non-scoped tokens (legacy / CLI), fall back to project membership check.
	if token.ProjectID == nil {
		user, err := s.store.GetUserByID(r.Context(), token.UserID)
		if err != nil || user == nil {
			return fmt.Errorf("user not found")
		}
		canAccess, _ := s.store.CanAccessProject(r.Context(), user.ID, projectID, user.Role == store.UserRoleAdmin)
		if !canAccess {
			return fmt.Errorf("access denied")
		}
	}
	return nil
}

// repoTree lists files in a hosted repo directory.
func (s *Server) repoTree(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHostedProject(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	path := r.URL.Query().Get("path")
	repoPath := gitrepo.RepoPath(s.gitRepoBasePath, p.ID)
	entries, err := gitrepo.ListTree(repoPath, ref, path)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if entries == nil {
		entries = []gitrepo.TreeEntry{}
	}
	jsonOK(w, entries)
}

// repoBlob returns the content of a file in a hosted repo.
func (s *Server) repoBlob(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHostedProject(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonErr(w, fmt.Errorf("path is required"), 400)
		return
	}
	repoPath := gitrepo.RepoPath(s.gitRepoBasePath, p.ID)
	content, err := gitrepo.ReadBlob(repoPath, ref, path)
	if err != nil {
		jsonErr(w, err, 404)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(content)
}

// repoCommits lists recent commits on a branch.
func (s *Server) repoCommits(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHostedProject(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	repoPath := gitrepo.RepoPath(s.gitRepoBasePath, p.ID)
	commits, err := gitrepo.ListCommits(repoPath, ref, limit)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if commits == nil {
		commits = []gitrepo.Commit{}
	}
	jsonOK(w, commits)
}

// repoBranches lists all branches in a hosted repo.
func (s *Server) repoBranches(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireHostedProject(w, r)
	if !ok {
		return
	}
	repoPath := gitrepo.RepoPath(s.gitRepoBasePath, p.ID)
	branches, err := gitrepo.ListBranches(repoPath)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if branches == nil {
		branches = []gitrepo.Branch{}
	}
	jsonOK(w, branches)
}

// requireHostedProject is a helper that extracts and validates the project
// for repo browser endpoints. Returns the project and true, or writes an
// error response and returns false.
func (s *Server) requireHostedProject(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	ok, _ := s.store.CanAccessProject(r.Context(), user.ID, id, user.Role == store.UserRoleAdmin)
	if !ok {
		jsonErr(w, nil, 404)
		return nil, false
	}
	p, err := s.store.GetProject(r.Context(), id)
	if err != nil || p == nil {
		jsonErr(w, err, 404)
		return nil, false
	}
	if p.GitSource != store.GitSourceHosted {
		jsonErr(w, fmt.Errorf("repository browser is only available for hosted repositories"), 400)
		return nil, false
	}
	if s.gitRepoBasePath == "" {
		jsonErr(w, fmt.Errorf("hosted git repositories are not enabled"), 400)
		return nil, false
	}
	return p, true
}
