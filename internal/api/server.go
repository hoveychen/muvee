package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/monitor"
	"github.com/hoveychen/muvee/internal/scheduler"
	"github.com/hoveychen/muvee/internal/store"
)

type Server struct {
	store            *store.Store
	auth             *auth.Service
	sched            *scheduler.Scheduler
	monitor          *monitor.Monitor
	baseDomain       string
	authServiceURL   string // base URL of muvee-authservice, e.g. http://muvee-authservice:4181
	agentSecret      string // shared secret for agent ↔ server authentication
	registryAddr     string // address of the Docker registry distributed to agents
	registryUser     string // registry basic-auth username distributed to agents
	registryPassword string // registry basic-auth password distributed to agents
	cliPending       sync.Map // state -> cli_port (string)
}

type ServerConfig struct {
	BaseDomain       string
	AuthServiceURL   string
	AgentSecret      string
	RegistryAddr     string
	RegistryUser     string
	RegistryPassword string
}

func NewServer(st *store.Store, authSvc *auth.Service, sched *scheduler.Scheduler, mon *monitor.Monitor, cfg ServerConfig) *Server {
	if cfg.AuthServiceURL == "" {
		cfg.AuthServiceURL = "http://muvee-authservice:4181"
	}
	return &Server{
		store:            st,
		auth:             authSvc,
		sched:            sched,
		monitor:          mon,
		baseDomain:       cfg.BaseDomain,
		authServiceURL:   cfg.AuthServiceURL,
		agentSecret:      cfg.AgentSecret,
		registryAddr:     cfg.RegistryAddr,
		registryUser:     cfg.RegistryUser,
		registryPassword: cfg.RegistryPassword,
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

	// Auth
	r.Get("/auth/google/login", s.handleGoogleLogin)
	r.Get("/auth/google/callback", s.handleGoogleCallback)
	r.Post("/auth/logout", s.handleLogout)

	// CLI device-flow auth
	r.Get("/auth/cli/login", s.handleCLILogin)

	// Public skill document for Claude
	r.Get("/api/skill", s.handleSkill)

	// Traefik HTTP provider – no auth, consumed only by Traefik on the internal network
	r.Get("/api/traefik/config", s.handleTraefikConfig)

	// Protected
	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		r.Get("/api/me", s.handleMe)

		// API Tokens
		r.Get("/api/tokens", s.listTokens)
		r.Post("/api/tokens", s.createToken)
		r.Delete("/api/tokens/{id}", s.deleteToken)

		// Projects
		r.Get("/api/projects", s.listProjects)
		r.Post("/api/projects", s.createProject)
		r.Get("/api/projects/{id}", s.getProject)
		r.Put("/api/projects/{id}", s.updateProject)
		r.Delete("/api/projects/{id}", s.deleteProject)
		r.Get("/api/projects/{id}/datasets", s.getProjectDatasets)
		r.Put("/api/projects/{id}/datasets", s.setProjectDatasets)
		r.Post("/api/projects/{id}/deploy", s.triggerDeploy)
		r.Get("/api/projects/{id}/deployments", s.listDeployments)

		// Datasets
		r.Get("/api/datasets", s.listDatasets)
		r.Post("/api/datasets", s.createDataset)
		r.Get("/api/datasets/{id}", s.getDataset)
		r.Put("/api/datasets/{id}", s.updateDataset)
		r.Delete("/api/datasets/{id}", s.deleteDataset)
		r.Post("/api/datasets/{id}/scan", s.scanDataset)
		r.Get("/api/datasets/{id}/snapshots", s.listSnapshots)
		r.Get("/api/datasets/{id}/history", s.listFileHistory)

		// Nodes (admin only)
		r.Group(func(r chi.Router) {
			r.Use(auth.AdminOnly)
			r.Get("/api/nodes", s.listNodes)
			r.Get("/api/users", s.listUsers)
			r.Put("/api/users/{id}/role", s.setUserRole)
		})
	})

	// Agent endpoints – protected by shared secret (X-Agent-Secret header)
	r.Group(func(r chi.Router) {
		r.Use(s.agentSecretMiddleware)
		r.Get("/api/agent/tasks", s.pollTasks)
		r.Post("/api/agent/tasks/{id}/complete", s.completeTask)
		r.Post("/api/agent/register", s.registerNode)
		r.Get("/api/agent/config", s.handleAgentConfig)
	})

	return r
}

// ─── Auth Handlers ───────────────────────────────────────────────────────────

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	state := uuid.New().String()
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, MaxAge: 300, HttpOnly: true, Path: "/"})
	http.Redirect(w, r, s.auth.AuthCodeURL(state), http.StatusFound)
}

// handleCLILogin initiates the device-flow OAuth for muveectl.
// The CLI passes ?port=PORT; we store the port keyed by state so the callback
// can redirect the token back to the local CLI server.
func (s *Server) handleCLILogin(w http.ResponseWriter, r *http.Request) {
	port := r.URL.Query().Get("port")
	if port == "" {
		http.Error(w, "port required", http.StatusBadRequest)
		return
	}
	state := uuid.New().String()
	s.cliPending.Store(state, port)
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, MaxAge: 300, HttpOnly: true, Path: "/"})
	http.Redirect(w, r, s.auth.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("oauth_state")
	if err != nil || cookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	state := cookie.Value
	user, jwtToken, err := s.auth.HandleCallback(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	// Check if this was initiated by the CLI device-flow
	if portVal, ok := s.cliPending.LoadAndDelete(state); ok {
		port := portVal.(string)
		apiToken, err := s.auth.CreateAPIToken(r.Context(), user.ID, "CLI Token")
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
// The server URL is inferred from the request so the login example is always correct.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	serverURL := scheme + "://" + r.Host
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

## API Tokens

` + "```" + `bash
muveectl tokens list
muveectl tokens create [--name NAME]   # token value shown once on creation
muveectl tokens delete TOKEN_ID
` + "```" + `

## Global Flags

| Flag | Description |
|------|-------------|
| ` + "`--server URL`" + ` | Override the configured server URL for this call |
| ` + "`--json`" + `      | Output raw JSON (pipe-friendly) |

## Git Repository Requirements

For a project to deploy successfully the repository must satisfy:

### Build
- Accessible via ` + "`git clone --depth=1`" + ` over HTTPS (public) or SSH (builder node must have the key)
- The configured branch must exist (default: ` + "`main`" + `)
- A ` + "`Dockerfile`" + ` must exist at the configured path (default: ` + "`Dockerfile`" + ` in repo root)
- Image must build for **` + "`linux/amd64`" + `** (` + "`docker buildx build --platform linux/amd64`" + `)

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

## Typical Workflow

1. Get project IDs: ` + "`muveectl projects list --json`" + `
2. Deploy a project: ` + "`muveectl projects deploy PROJECT_ID`" + `
3. Check status: ` + "`muveectl projects deployments PROJECT_ID`" + `
`

// ─── API Tokens ──────────────────────────────────────────────────────────────

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	tokens, err := s.store.ListAPITokensForUser(r.Context(), user.ID)
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

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err, 400)
		return
	}
	if body.Name == "" {
		body.Name = "CLI Token"
	}
	token, err := s.auth.CreateAPIToken(r.Context(), user.ID, body.Name)
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

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := mustParseUUID(chi.URLParam(r, "id"))
	user := auth.UserFromCtx(r.Context())
	if err := s.store.DeleteAPIToken(r.Context(), id, user.ID); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ─── Projects ────────────────────────────────────────────────────────────────

// reservedDomainPrefixes are subdomain prefixes occupied by system services.
// User-created projects must not use these names to avoid routing conflicts.
var reservedDomainPrefixes = map[string]bool{
	"www":      true,
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
	if p.DomainPrefix == "" {
		if err := validateDomainPrefix(p.Name); err != nil {
			return fmt.Errorf("domain_prefix is required because project name %q cannot be used as a subdomain: %w", p.Name, err)
		}
		p.DomainPrefix = p.Name
		return nil
	}
	return validateDomainPrefix(p.DomainPrefix)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromCtx(r.Context())
	projects, err := s.store.ListProjectsForUser(r.Context(), user.ID, user.Role == store.UserRoleAdmin)
	if err != nil {
		jsonErr(w, err, 500)
		return
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
	p.OwnerID = user.ID
	created, err := s.store.CreateProject(r.Context(), &p)
	if err != nil {
		jsonErr(w, err, 500)
		return
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
	user := auth.UserFromCtx(r.Context())
	var d store.Dataset
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonErr(w, err, 400)
		return
	}
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
	d.ID = id
	if err := s.store.UpdateDataset(r.Context(), &d); err != nil {
		jsonErr(w, err, 500)
		return
	}
	jsonOK(w, d)
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
		"registry_addr":     s.registryAddr,
		"registry_user":     s.registryUser,
		"registry_password": s.registryPassword,
		"base_domain":       s.baseDomain,
	})
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
	if err := s.store.UpdateTaskStatus(r.Context(), taskID, status, body.Result); err != nil {
		jsonErr(w, err, 500)
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
			// Retire previous running deployments for the same project
			if dep, err := s.store.GetDeployment(r.Context(), task.DeploymentID); err == nil && dep != nil {
				_ = s.store.StopProjectDeployments(r.Context(), dep.ProjectID, task.DeploymentID)
			}
		} else {
			_ = s.store.UpdateDeploymentStatus(r.Context(), task.DeploymentID, store.DeploymentStatusFailed, "deploy completed but no host_port reported")
		}
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
	Middlewares map[string]traefikMiddleware `json:"middlewares"`
}

type traefikRouter struct {
	Rule        string   `json:"rule"`
	EntryPoints []string `json:"entryPoints"`
	Service     string   `json:"service"`
	Middlewares []string `json:"middlewares,omitempty"`
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
			Routers:     make(map[string]traefikRouter),
			Services:    make(map[string]traefikService),
			Middlewares: make(map[string]traefikMiddleware),
		},
	}

	for _, dep := range deployments {
		name := dep.DomainPrefix
		host := fmt.Sprintf("%s.%s", dep.DomainPrefix, s.baseDomain)
		backendURL := fmt.Sprintf("http://%s:%d", dep.HostIP, dep.HostPort)

		// HTTPS router
		httpsRouter := traefikRouter{
			Rule:        fmt.Sprintf("Host(`%s`)", host),
			EntryPoints: []string{"websecure"},
			Service:     name,
			TLS:         &traefikTLS{CertResolver: "letsencrypt"},
		}

		// HTTP router (redirects to HTTPS via the middleware in dynamic.yml)
		httpRouter := traefikRouter{
			Rule:        fmt.Sprintf("Host(`%s`)", host),
			EntryPoints: []string{"web"},
			Service:     name,
			Middlewares: []string{"redirect-to-https@file"},
		}

		// Per-project ForwardAuth middleware (if auth is required)
		if dep.AuthRequired {
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
		cfg.HTTP.Routers[name+"-http"] = httpRouter
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
