package api

import (
	"encoding/json"
	"net/http"
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
	store     *store.Store
	auth      *auth.Service
	sched     *scheduler.Scheduler
	monitor   *monitor.Monitor
	baseDomain string
}

func NewServer(st *store.Store, authSvc *auth.Service, sched *scheduler.Scheduler, mon *monitor.Monitor, baseDomain string) *Server {
	return &Server{store: st, auth: authSvc, sched: sched, monitor: mon, baseDomain: baseDomain}
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

	// Protected
	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		r.Get("/api/me", s.handleMe)

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

	// Agent endpoints (use node token auth)
	r.Get("/api/agent/tasks", s.pollTasks)
	r.Post("/api/agent/tasks/{id}/complete", s.completeTask)
	r.Post("/api/agent/register", s.registerNode)

	return r
}

// ─── Auth Handlers ───────────────────────────────────────────────────────────

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	state := uuid.New().String()
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, MaxAge: 300, HttpOnly: true, Path: "/"})
	http.Redirect(w, r, s.auth.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("oauth_state")
	if err != nil || cookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	user, token, err := s.auth.HandleCallback(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	_ = user
	http.SetCookie(w, &http.Cookie{
		Name: "muvee_session", Value: token,
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

// ─── Projects ────────────────────────────────────────────────────────────────

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
		Status  string `json:"status"`
		Result  string `json:"result"`
		ImageTag string `json:"image_tag"` // for build tasks
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

	// If build completed, dispatch deploy
	if status == store.TaskStatusCompleted && body.ImageTag != "" {
		tasks, _ := s.store.PollTasksForNode(r.Context(), uuid.Nil)
		_ = tasks
		// Re-fetch task to get deployment ID
		_ = taskID
	}
	jsonOK(w, map[string]string{"status": "ok"})
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
