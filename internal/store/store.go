package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hoveychen/muvee/internal/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db            *pgxpool.Pool
	encryptionKey []byte // 32-byte AES-256-GCM key; may be nil (secrets disabled)
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func NewWithEncryption(db *pgxpool.Pool, encryptionKey []byte) *Store {
	return &Store{db: db, encryptionKey: encryptionKey}
}

func (s *Store) SecretsEnabled() bool {
	return s.encryptionKey != nil
}

func (s *Store) DB() *pgxpool.Pool {
	return s.db
}

// ─── Users ───────────────────────────────────────────────────────────────────

func (s *Store) UpsertUser(ctx context.Context, email, name, avatarURL string) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		INSERT INTO users (id, email, name, avatar_url, role, created_at)
		VALUES ($1, $2, $3, $4, 'member', NOW())
		ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name, avatar_url = EXCLUDED.avatar_url
		RETURNING id, email, name, avatar_url, role, authorized, created_at
	`, uuid.New(), email, name, avatarURL).Scan(
		&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.Authorized, &u.CreatedAt,
	)
	return &u, err
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, avatar_url, role, authorized, created_at FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.Authorized, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.Query(ctx, `SELECT id, email, name, avatar_url, role, authorized, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]*User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.Authorized, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, &u)
	}
	return users, nil
}

func (s *Store) SetUserRole(ctx context.Context, id uuid.UUID, role UserRole) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET role = $1 WHERE id = $2`, role, id)
	return err
}

// ─── Projects ────────────────────────────────────────────────────────────────

// projectColumns is the full SELECT list for a Project row. git_url is COALESCEd
// so domain_only projects (which have NULL git_url) scan into an empty string.
const projectColumns = `id, name, project_type, COALESCE(git_url, '') AS git_url, git_branch, git_source, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, auth_bypass_paths, container_port, memory_limit, volume_mount_path, description, icon, tags, created_at, updated_at`

func scanProject(scanner interface {
	Scan(dest ...interface{}) error
}, p *Project) error {
	return scanner.Scan(&p.ID, &p.Name, &p.ProjectType, &p.GitURL, &p.GitBranch, &p.GitSource, &p.DomainPrefix, &p.DockerfilePath, &p.OwnerID, &p.AuthRequired, &p.AuthAllowedDomains, &p.AuthBypassPaths, &p.ContainerPort, &p.MemoryLimit, &p.VolumeMountPath, &p.Description, &p.Icon, &p.Tags, &p.CreatedAt, &p.UpdatedAt)
}

func (s *Store) CreateProject(ctx context.Context, p *Project) (*Project, error) {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	if p.ProjectType == "" {
		p.ProjectType = ProjectTypeDeployment
	}
	if p.ProjectType == ProjectTypeDeployment {
		if p.ContainerPort == 0 {
			p.ContainerPort = 8080
		}
		if p.MemoryLimit == "" {
			p.MemoryLimit = "4g"
		}
		if p.GitSource == "" {
			p.GitSource = GitSourceExternal
		}
	}
	// git_url is stored as NULL when empty so the NOT NULL-dropped column
	// stays consistent semantically for domain_only rows.
	var gitURL interface{}
	if p.GitURL != "" {
		gitURL = p.GitURL
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO projects (id, name, project_type, git_url, git_branch, git_source, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, auth_bypass_paths, container_port, memory_limit, volume_mount_path, description, icon, tags, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`, p.ID, p.Name, p.ProjectType, gitURL, p.GitBranch, p.GitSource, p.DomainPrefix, p.DockerfilePath, p.OwnerID, p.AuthRequired, p.AuthAllowedDomains, p.AuthBypassPaths, p.ContainerPort, p.MemoryLimit, p.VolumeMountPath, p.Description, p.Icon, p.Tags, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO project_members (project_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p.ID, p.OwnerID)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (*Project, error) {
	var p Project
	err := scanProject(s.db.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1`, id), &p)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

func (s *Store) ListProjectsForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]*Project, error) {
	var query string
	var args []interface{}
	if isAdmin {
		query = `SELECT ` + projectColumns + ` FROM projects ORDER BY created_at DESC`
	} else {
		query = `SELECT ` + projectColumns + `
			FROM projects p JOIN project_members pm ON p.id = pm.project_id WHERE pm.user_id = $1 ORDER BY p.created_at DESC`
		args = []interface{}{userID}
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := make([]*Project, 0)
	for rows.Next() {
		var p Project
		if err := scanProject(rows, &p); err != nil {
			return nil, err
		}
		projects = append(projects, &p)
	}
	return projects, nil
}

func (s *Store) UpdateProject(ctx context.Context, p *Project) error {
	p.UpdatedAt = time.Now()
	if p.ProjectType == ProjectTypeDeployment && p.ContainerPort == 0 {
		p.ContainerPort = 8080
	}
	var gitURL interface{}
	if p.GitURL != "" {
		gitURL = p.GitURL
	}
	_, err := s.db.Exec(ctx, `
		UPDATE projects SET name=$1, git_url=$2, git_branch=$3, git_source=$4, domain_prefix=$5, dockerfile_path=$6, auth_required=$7, auth_allowed_domains=$8, auth_bypass_paths=$9, container_port=$10, memory_limit=$11, volume_mount_path=$12, description=$13, icon=$14, tags=$15, updated_at=$16 WHERE id=$17
	`, p.Name, gitURL, p.GitBranch, p.GitSource, p.DomainPrefix, p.DockerfilePath, p.AuthRequired, p.AuthAllowedDomains, p.AuthBypassPaths, p.ContainerPort, p.MemoryLimit, p.VolumeMountPath, p.Description, p.Icon, p.Tags, p.UpdatedAt, p.ID)
	return err
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
}

// ListPublicRunningProjects returns a minimal view of all projects that currently
// have a running deployment. No authentication is required to call this; the
// returned fields are safe to expose publicly.
func (s *Store) ListPublicRunningProjects(ctx context.Context) ([]*PublicProjectInfo, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (p.id)
		       p.id, p.name, p.domain_prefix, p.description, p.icon, p.tags,
		       p.auth_required, p.updated_at,
		       u.name AS owner_name, u.avatar_url AS owner_avatar_url
		FROM projects p
		JOIN deployments d ON d.project_id = p.id AND d.status = 'running'
		JOIN users u ON u.id = p.owner_id
		ORDER BY p.id, p.updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*PublicProjectInfo, 0)
	for rows.Next() {
		var info PublicProjectInfo
		if err := rows.Scan(
			&info.ID, &info.Name, &info.DomainPrefix, &info.Description, &info.Icon, &info.Tags,
			&info.AuthRequired, &info.UpdatedAt,
			&info.OwnerName, &info.OwnerAvatarURL,
		); err != nil {
			return nil, err
		}
		items = append(items, &info)
	}
	return items, nil
}

func (s *Store) GetProjectByDomainPrefix(ctx context.Context, prefix string) (*Project, error) {
	var p Project
	err := scanProject(s.db.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE domain_prefix = $1`, prefix), &p)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

// GetProjectByOwnerAndName looks up a project by the combination of owner and
// project name. Names are unique per owner (enforced by the
// projects_owner_name_key constraint), so this returns at most one row.
func (s *Store) GetProjectByOwnerAndName(ctx context.Context, ownerID uuid.UUID, name string) (*Project, error) {
	var p Project
	err := scanProject(s.db.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE owner_id = $1 AND name = $2`, ownerID, name), &p)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

// ListPublicDomainOnlyProjects returns a minimal public view of all domain_only
// projects. The caller is responsible for filtering by tunnel liveness.
func (s *Store) ListPublicDomainOnlyProjects(ctx context.Context) ([]*PublicProjectInfo, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.name, p.domain_prefix, p.description, p.icon, p.tags,
		       p.auth_required, p.updated_at,
		       u.name AS owner_name, u.avatar_url AS owner_avatar_url
		FROM projects p
		JOIN users u ON u.id = p.owner_id
		WHERE p.project_type = 'domain_only'
		ORDER BY p.updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*PublicProjectInfo, 0)
	for rows.Next() {
		var info PublicProjectInfo
		if err := rows.Scan(
			&info.ID, &info.Name, &info.DomainPrefix, &info.Description, &info.Icon, &info.Tags,
			&info.AuthRequired, &info.UpdatedAt,
			&info.OwnerName, &info.OwnerAvatarURL,
		); err != nil {
			return nil, err
		}
		items = append(items, &info)
	}
	return items, nil
}

// ListDomainOnlyProjects returns every project with project_type = 'domain_only'.
// Used by the Traefik config generator to emit a stable route for each reserved
// domain so that the offline placeholder can be served when no tunnel is live.
func (s *Store) ListDomainOnlyProjects(ctx context.Context) ([]*Project, error) {
	rows, err := s.db.Query(ctx, `SELECT `+projectColumns+` FROM projects WHERE project_type = 'domain_only' ORDER BY domain_prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Project, 0)
	for rows.Next() {
		var p Project
		if err := scanProject(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, nil
}

func (s *Store) CanAccessProject(ctx context.Context, userID, projectID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&count)
	return count > 0, err
}

// ─── Project Datasets ────────────────────────────────────────────────────────

func (s *Store) SetProjectDatasets(ctx context.Context, projectID uuid.UUID, items []ProjectDataset) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM project_datasets WHERE project_id = $1`, projectID); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO project_datasets (project_id, dataset_id, mount_mode) VALUES ($1,$2,$3)`, projectID, item.DatasetID, item.MountMode); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) GetProjectDatasets(ctx context.Context, projectID uuid.UUID) ([]ProjectDataset, error) {
	rows, err := s.db.Query(ctx, `SELECT project_id, dataset_id, mount_mode FROM project_datasets WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProjectDataset, 0)
	for rows.Next() {
		var item ProjectDataset
		if err := rows.Scan(&item.ProjectID, &item.DatasetID, &item.MountMode); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// ─── Datasets ────────────────────────────────────────────────────────────────

func (s *Store) CreateDataset(ctx context.Context, d *Dataset) (*Dataset, error) {
	d.ID = uuid.New()
	d.Version = 1
	d.CreatedAt = time.Now()
	d.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO datasets (id, name, nfs_path, size_bytes, checksum, version, owner_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, d.ID, d.Name, d.NFSPath, d.SizeBytes, d.Checksum, d.Version, d.OwnerID, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO dataset_members (dataset_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, d.ID, d.OwnerID)
	return d, err
}

func (s *Store) GetDataset(ctx context.Context, id uuid.UUID) (*Dataset, error) {
	var d Dataset
	err := s.db.QueryRow(ctx, `
		SELECT id, name, nfs_path, size_bytes, checksum, version, owner_id, created_at, updated_at FROM datasets WHERE id = $1
	`, id).Scan(&d.ID, &d.Name, &d.NFSPath, &d.SizeBytes, &d.Checksum, &d.Version, &d.OwnerID, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &d, err
}

func (s *Store) ListDatasetsForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]*Dataset, error) {
	var query string
	var args []interface{}
	if isAdmin {
		query = `SELECT id, name, nfs_path, size_bytes, checksum, version, owner_id, created_at, updated_at FROM datasets ORDER BY created_at DESC`
	} else {
		query = `SELECT d.id, d.name, d.nfs_path, d.size_bytes, d.checksum, d.version, d.owner_id, d.created_at, d.updated_at
			FROM datasets d JOIN dataset_members dm ON d.id = dm.dataset_id WHERE dm.user_id = $1 ORDER BY d.created_at DESC`
		args = []interface{}{userID}
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	datasets := make([]*Dataset, 0)
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.NFSPath, &d.SizeBytes, &d.Checksum, &d.Version, &d.OwnerID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		datasets = append(datasets, &d)
	}
	return datasets, nil
}

func (s *Store) UpdateDataset(ctx context.Context, d *Dataset) error {
	d.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		UPDATE datasets SET name=$1, nfs_path=$2, size_bytes=$3, checksum=$4, version=$5, updated_at=$6 WHERE id=$7
	`, d.Name, d.NFSPath, d.SizeBytes, d.Checksum, d.Version, d.UpdatedAt, d.ID)
	return err
}

func (s *Store) IncrementDatasetVersion(ctx context.Context, id uuid.UUID) (int64, error) {
	var version int64
	err := s.db.QueryRow(ctx, `UPDATE datasets SET version = version + 1, updated_at = NOW() WHERE id = $1 RETURNING version`, id).Scan(&version)
	return version, err
}

func (s *Store) DeleteDataset(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM datasets WHERE id = $1`, id)
	return err
}

func (s *Store) CanAccessDataset(ctx context.Context, userID, datasetID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM dataset_members WHERE dataset_id=$1 AND user_id=$2`, datasetID, userID).Scan(&count)
	return count > 0, err
}

// ─── Deployments ─────────────────────────────────────────────────────────────

func (s *Store) CreateDeployment(ctx context.Context, d *Deployment) (*Deployment, error) {
	d.ID = uuid.New()
	d.Status = DeploymentStatusPending
	d.CreatedAt = time.Now()
	d.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO deployments (id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, restart_count, oom_killed, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, d.ID, d.ProjectID, d.ImageTag, d.CommitSHA, d.Status, d.NodeID, d.HostPort, d.Logs, d.RestartCount, d.OOMKilled, d.CreatedAt, d.UpdatedAt)
	return d, err
}

func (s *Store) GetDeployment(ctx context.Context, id uuid.UUID) (*Deployment, error) {
	var d Deployment
	err := s.db.QueryRow(ctx, `
		SELECT id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, restart_count, oom_killed, created_at, updated_at FROM deployments WHERE id = $1
	`, id).Scan(&d.ID, &d.ProjectID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.NodeID, &d.HostPort, &d.Logs, &d.RestartCount, &d.OOMKilled, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &d, err
}

func (s *Store) ListDeployments(ctx context.Context, projectID uuid.UUID) ([]*Deployment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, restart_count, oom_killed, created_at, updated_at FROM deployments WHERE project_id = $1 ORDER BY created_at DESC LIMIT 50
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deployments := make([]*Deployment, 0)
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.NodeID, &d.HostPort, &d.Logs, &d.RestartCount, &d.OOMKilled, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		deployments = append(deployments, &d)
	}
	return deployments, nil
}

// SetDeploymentImageTag stores the image tag built for a deployment.
func (s *Store) SetDeploymentImageTag(ctx context.Context, id uuid.UUID, imageTag string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments SET image_tag=$1, updated_at=NOW() WHERE id=$2
	`, imageTag, id)
	return err
}

// SetDeploymentHostPort stores the host port after a successful deploy and marks status as running.
func (s *Store) SetDeploymentHostPort(ctx context.Context, id uuid.UUID, hostPort int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments SET host_port=$1, status='running', updated_at=NOW() WHERE id=$2
	`, hostPort, id)
	return err
}

// StopProjectDeployments marks all running deployments for a project as stopped, except the given one.
// Returns the list of deployments that were stopped (useful for dispatching cross-node cleanup tasks).
func (s *Store) StopProjectDeployments(ctx context.Context, projectID, exceptID uuid.UUID) ([]*Deployment, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE deployments SET status='stopped', updated_at=NOW()
		WHERE project_id=$1 AND id != $2 AND status='running'
		RETURNING id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, restart_count, oom_killed, created_at, updated_at
	`, projectID, exceptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stopped []*Deployment
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.NodeID, &d.HostPort, &d.Logs, &d.RestartCount, &d.OOMKilled, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		stopped = append(stopped, &d)
	}
	return stopped, nil
}

// GetRunningDeploymentsByNode returns all running deployments assigned to a specific node.
func (s *Store) GetRunningDeploymentsByNode(ctx context.Context, nodeID uuid.UUID) ([]*Deployment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, restart_count, oom_killed, created_at, updated_at
		FROM deployments WHERE node_id=$1 AND status='running'
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []*Deployment
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.NodeID, &d.HostPort, &d.Logs, &d.RestartCount, &d.OOMKilled, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		deps = append(deps, &d)
	}
	return deps, nil
}

// UpdateDeploymentRuntimeStatus updates the restart count and OOM-killed flag for the
// currently running deployment of a project identified by domain prefix.
// restart_count is updated to the maximum of the stored and reported value (monotonically increasing).
// oom_killed is sticky: once true it stays true.
func (s *Store) UpdateDeploymentRuntimeStatus(ctx context.Context, domainPrefix string, restartCount int, oomKilled bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments d
		SET restart_count = GREATEST(d.restart_count, $1),
		    oom_killed    = d.oom_killed OR $2,
		    updated_at    = NOW()
		FROM projects p
		WHERE d.project_id = p.id
		  AND p.domain_prefix = $3
		  AND d.status = 'running'
	`, restartCount, oomKilled, domainPrefix)
	return err
}

// GetTask retrieves a single task by ID.
func (s *Store) GetTask(ctx context.Context, id uuid.UUID) (*Task, error) {
	var t Task
	err := s.db.QueryRow(ctx, `
		SELECT id, type, node_id, deployment_id, payload, status, result, created_at, updated_at FROM tasks WHERE id = $1
	`, id).Scan(&t.ID, &t.Type, &t.NodeID, &t.DeploymentID, &t.PayloadJSON, &t.Status, &t.Result, &t.CreatedAt, &t.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(t.PayloadJSON), &t.Payload)
	return &t, nil
}

// GetRunningDeployments returns all running deployments with the info needed to build Traefik routes.
func (s *Store) GetRunningDeployments(ctx context.Context) ([]*RunningDeploymentInfo, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.id, d.project_id, p.domain_prefix, p.auth_required, p.auth_allowed_domains, p.auth_bypass_paths, n.host_ip, d.host_port
		FROM deployments d
		JOIN projects p ON d.project_id = p.id
		JOIN nodes n ON d.node_id = n.id
		WHERE d.status = 'running' AND d.host_port > 0 AND n.host_ip != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*RunningDeploymentInfo, 0)
	for rows.Next() {
		var r RunningDeploymentInfo
		if err := rows.Scan(&r.DeploymentID, &r.ProjectID, &r.DomainPrefix, &r.AuthRequired, &r.AuthAllowedDomains, &r.AuthBypassPaths, &r.HostIP, &r.HostPort); err != nil {
			return nil, err
		}
		items = append(items, &r)
	}
	return items, nil
}

// GetRunningDeploymentByProject returns the running deployment info for a specific project.
// Returns nil if no running deployment exists.
func (s *Store) GetRunningDeploymentByProject(ctx context.Context, projectID uuid.UUID) (*RunningDeploymentInfo, error) {
	var r RunningDeploymentInfo
	err := s.db.QueryRow(ctx, `
		SELECT d.id, d.project_id, p.domain_prefix, p.auth_required, p.auth_allowed_domains, p.auth_bypass_paths, n.host_ip, d.host_port
		FROM deployments d
		JOIN projects p ON d.project_id = p.id
		JOIN nodes n ON d.node_id = n.id
		WHERE d.project_id = $1 AND d.status = 'running' AND d.host_port > 0 AND n.host_ip != ''
		ORDER BY d.created_at DESC
		LIMIT 1
	`, projectID).Scan(&r.DeploymentID, &r.ProjectID, &r.DomainPrefix, &r.AuthRequired, &r.AuthAllowedDomains, &r.AuthBypassPaths, &r.HostIP, &r.HostPort)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) UpdateDeploymentStatus(ctx context.Context, id uuid.UUID, status DeploymentStatus, errMsg string) error {
	if errMsg != "" {
		_, err := s.db.Exec(ctx, `UPDATE deployments SET status=$1, logs=logs||$2, updated_at=NOW() WHERE id=$3`, status, errMsg+"\n", id)
		return err
	}
	_, err := s.db.Exec(ctx, `UPDATE deployments SET status=$1, updated_at=NOW() WHERE id=$2`, status, id)
	return err
}

func (s *Store) AppendDeploymentLog(ctx context.Context, id uuid.UUID, line string) error {
	_, err := s.db.Exec(ctx, `UPDATE deployments SET logs = logs || $1, updated_at = NOW() WHERE id = $2`, line+"\n", id)
	return err
}

func (s *Store) SetDeploymentNode(ctx context.Context, id, nodeID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE deployments SET node_id=$1, updated_at=NOW() WHERE id=$2`, nodeID, id)
	return err
}

// ─── Nodes ───────────────────────────────────────────────────────────────────

func (s *Store) UpsertNode(ctx context.Context, n *Node) (*Node, error) {
	n.LastSeenAt = time.Now()
	var existing Node
	err := s.db.QueryRow(ctx, `SELECT id FROM nodes WHERE hostname = $1 AND role = $2`, n.Hostname, n.Role).Scan(&existing.ID)
	if err == pgx.ErrNoRows {
		n.ID = uuid.New()
		n.CreatedAt = time.Now()
		_, err = s.db.Exec(ctx, `
			INSERT INTO nodes (id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, n.ID, n.Hostname, n.Role, n.HostIP, n.MaxStorageBytes, n.UsedStorageBytes, n.LastSeenAt, n.CreatedAt)
	} else if err == nil {
		n.ID = existing.ID
		_, err = s.db.Exec(ctx, `UPDATE nodes SET host_ip=$1, max_storage_bytes=$2, last_seen_at=$3 WHERE id=$4`,
			n.HostIP, n.MaxStorageBytes, n.LastSeenAt, n.ID)
	}
	return n, err
}

func (s *Store) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.Query(ctx, `SELECT id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at, health_report FROM nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]*Node, 0)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Hostname, &n.Role, &n.HostIP, &n.MaxStorageBytes, &n.UsedStorageBytes, &n.LastSeenAt, &n.CreatedAt, &n.HealthReport); err != nil {
			return nil, err
		}
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func (s *Store) GetDeployNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.Query(ctx, `SELECT id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at, health_report FROM nodes WHERE role = 'deploy' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := make([]*Node, 0)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Hostname, &n.Role, &n.HostIP, &n.MaxStorageBytes, &n.UsedStorageBytes, &n.LastSeenAt, &n.CreatedAt, &n.HealthReport); err != nil {
			return nil, err
		}
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func (s *Store) TouchNode(ctx context.Context, nodeID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `UPDATE nodes SET last_seen_at = $1 WHERE id = $2`, time.Now(), nodeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}
	return nil
}

func (s *Store) DeleteNode(ctx context.Context, nodeID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
	return err
}

// ─── Node Datasets ───────────────────────────────────────────────────────────

func (s *Store) GetNodeDatasets(ctx context.Context, nodeID uuid.UUID) ([]*NodeDataset, error) {
	rows, err := s.db.Query(ctx, `SELECT node_id, dataset_id, last_used_at, size_bytes FROM node_datasets WHERE node_id = $1`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*NodeDataset, 0)
	for rows.Next() {
		var nd NodeDataset
		if err := rows.Scan(&nd.NodeID, &nd.DatasetID, &nd.LastUsedAt, &nd.SizeBytes); err != nil {
			return nil, err
		}
		items = append(items, &nd)
	}
	return items, nil
}

func (s *Store) TouchNodeDataset(ctx context.Context, nodeID, datasetID uuid.UUID, sizeBytes int64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO node_datasets (node_id, dataset_id, last_used_at, size_bytes) VALUES ($1,$2,NOW(),$3)
		ON CONFLICT (node_id, dataset_id) DO UPDATE SET last_used_at=NOW(), size_bytes=$3
	`, nodeID, datasetID, sizeBytes)
	return err
}

func (s *Store) RemoveNodeDataset(ctx context.Context, nodeID, datasetID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM node_datasets WHERE node_id=$1 AND dataset_id=$2`, nodeID, datasetID)
	return err
}

func (s *Store) GetLRUDatasetsForNode(ctx context.Context, nodeID uuid.UUID, needed int64) ([]*NodeDataset, error) {
	rows, err := s.db.Query(ctx, `
		SELECT node_id, dataset_id, last_used_at, size_bytes FROM node_datasets WHERE node_id = $1 ORDER BY last_used_at ASC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*NodeDataset, 0)
	var freed int64
	for rows.Next() && freed < needed {
		var nd NodeDataset
		if err := rows.Scan(&nd.NodeID, &nd.DatasetID, &nd.LastUsedAt, &nd.SizeBytes); err != nil {
			return nil, err
		}
		items = append(items, &nd)
		freed += nd.SizeBytes
	}
	return items, nil
}

// ─── Tasks ───────────────────────────────────────────────────────────────────

func (s *Store) CreateTask(ctx context.Context, t *Task) (*Task, error) {
	t.ID = uuid.New()
	t.Status = TaskStatusPending
	t.CreatedAt = time.Now()
	t.UpdatedAt = time.Now()
	payload, err := json.Marshal(t.Payload)
	if err != nil {
		return nil, err
	}
	t.PayloadJSON = string(payload)
	_, err = s.db.Exec(ctx, `
		INSERT INTO tasks (id, type, node_id, deployment_id, payload, status, result, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, t.ID, t.Type, t.NodeID, t.DeploymentID, t.PayloadJSON, t.Status, t.Result, t.CreatedAt, t.UpdatedAt)
	return t, err
}

func (s *Store) PollTasksForNode(ctx context.Context, nodeID uuid.UUID) ([]*Task, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, type, node_id, deployment_id, payload, status, result, created_at, updated_at
		FROM tasks WHERE node_id = $1 AND status = 'pending' ORDER BY created_at ASC LIMIT 10
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]*Task, 0)
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Type, &t.NodeID, &t.DeploymentID, &t.PayloadJSON, &t.Status, &t.Result, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(t.PayloadJSON), &t.Payload)
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

func (s *Store) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus, result string) error {
	_, err := s.db.Exec(ctx, `UPDATE tasks SET status=$1, result=$2, updated_at=NOW() WHERE id=$3`, status, result, id)
	return err
}

// ─── Dataset Snapshots & File History ────────────────────────────────────────

func (s *Store) CreateDatasetSnapshot(ctx context.Context, snap *DatasetSnapshot) (*DatasetSnapshot, error) {
	snap.ID = uuid.New()
	snap.ScannedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO dataset_snapshots (id, dataset_id, scanned_at, total_files, total_size_bytes, version)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, snap.ID, snap.DatasetID, snap.ScannedAt, snap.TotalFiles, snap.TotalSizeBytes, snap.Version)
	return snap, err
}

func (s *Store) GetLatestSnapshot(ctx context.Context, datasetID uuid.UUID) (*DatasetSnapshot, error) {
	var snap DatasetSnapshot
	err := s.db.QueryRow(ctx, `
		SELECT id, dataset_id, scanned_at, total_files, total_size_bytes, version FROM dataset_snapshots WHERE dataset_id = $1 ORDER BY scanned_at DESC LIMIT 1
	`, datasetID).Scan(&snap.ID, &snap.DatasetID, &snap.ScannedAt, &snap.TotalFiles, &snap.TotalSizeBytes, &snap.Version)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &snap, err
}

func (s *Store) ListSnapshotsForDataset(ctx context.Context, datasetID uuid.UUID) ([]*DatasetSnapshot, error) {
	rows, err := s.db.Query(ctx, `SELECT id, dataset_id, scanned_at, total_files, total_size_bytes, version FROM dataset_snapshots WHERE dataset_id = $1 ORDER BY scanned_at DESC LIMIT 100`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snaps := make([]*DatasetSnapshot, 0)
	for rows.Next() {
		var s DatasetSnapshot
		if err := rows.Scan(&s.ID, &s.DatasetID, &s.ScannedAt, &s.TotalFiles, &s.TotalSizeBytes, &s.Version); err != nil {
			return nil, err
		}
		snaps = append(snaps, &s)
	}
	return snaps, nil
}

func (s *Store) BulkInsertFileHistory(ctx context.Context, items []*DatasetFileHistory) error {
	if len(items) == 0 {
		return nil
	}
	valueStrings := make([]string, 0, len(items))
	valueArgs := make([]interface{}, 0, len(items)*10)
	for i, item := range items {
		item.ID = uuid.New()
		item.OccurredAt = time.Now()
		base := i * 10
		valueStrings = append(valueStrings, fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10))
		valueArgs = append(valueArgs, item.ID, item.DatasetID, item.FilePath, item.EventType,
			item.OldSize, item.NewSize, item.OldChecksum, item.NewChecksum, item.SnapshotID, item.OccurredAt)
	}
	query := `INSERT INTO dataset_file_history (id, dataset_id, file_path, event_type, old_size, new_size, old_checksum, new_checksum, snapshot_id, occurred_at) VALUES ` + strings.Join(valueStrings, ",")
	_, err := s.db.Exec(ctx, query, valueArgs...)
	return err
}

func (s *Store) ListFileHistory(ctx context.Context, datasetID uuid.UUID, filePath string, limit int) ([]*DatasetFileHistory, error) {
	var rows pgx.Rows
	var err error
	if filePath != "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, dataset_id, file_path, event_type, old_size, new_size, old_checksum, new_checksum, snapshot_id, occurred_at
			FROM dataset_file_history WHERE dataset_id=$1 AND file_path=$2 ORDER BY occurred_at DESC LIMIT $3
		`, datasetID, filePath, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, dataset_id, file_path, event_type, old_size, new_size, old_checksum, new_checksum, snapshot_id, occurred_at
			FROM dataset_file_history WHERE dataset_id=$1 ORDER BY occurred_at DESC LIMIT $2
		`, datasetID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*DatasetFileHistory, 0)
	for rows.Next() {
		var h DatasetFileHistory
		if err := rows.Scan(&h.ID, &h.DatasetID, &h.FilePath, &h.EventType, &h.OldSize, &h.NewSize, &h.OldChecksum, &h.NewChecksum, &h.SnapshotID, &h.OccurredAt); err != nil {
			return nil, err
		}
		items = append(items, &h)
	}
	return items, nil
}

func (s *Store) PruneFileHistory(ctx context.Context, datasetID uuid.UUID, before time.Time) error {
	_, err := s.db.Exec(ctx, `DELETE FROM dataset_file_history WHERE dataset_id=$1 AND occurred_at < $2`, datasetID, before)
	return err
}

// ─── Node Metrics ─────────────────────────────────────────────────────────────

// InsertNodeMetric inserts a host-level resource sample for the given node.
// Rows older than 24 hours for the same node are pruned on insert.
func (s *Store) InsertNodeMetric(ctx context.Context, m *NodeMetric) error {
	m.ID = uuid.New()
	m.CollectedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO node_metrics
		  (id, node_id, collected_at, cpu_percent,
		   mem_total_bytes, mem_used_bytes,
		   disk_total_bytes, disk_used_bytes,
		   load1, load5, load15)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, m.ID, m.NodeID, m.CollectedAt,
		m.CPUPercent,
		m.MemTotalBytes, m.MemUsedBytes,
		m.DiskTotalBytes, m.DiskUsedBytes,
		m.Load1, m.Load5, m.Load15)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(ctx,
		`DELETE FROM node_metrics WHERE node_id=$1 AND collected_at < NOW() - INTERVAL '24 hours'`,
		m.NodeID)
	return nil
}

// GetLatestNodeMetricByNodeID returns the most recent metric sample for the given node,
// or nil if no samples exist.
func (s *Store) GetLatestNodeMetricByNodeID(ctx context.Context, nodeID uuid.UUID) (*NodeMetric, error) {
	var m NodeMetric
	err := s.db.QueryRow(ctx, `
		SELECT id, node_id, collected_at, cpu_percent,
		       mem_total_bytes, mem_used_bytes,
		       disk_total_bytes, disk_used_bytes,
		       load1, load5, load15
		FROM node_metrics
		WHERE node_id = $1
		ORDER BY collected_at DESC
		LIMIT 1
	`, nodeID).Scan(
		&m.ID, &m.NodeID, &m.CollectedAt, &m.CPUPercent,
		&m.MemTotalBytes, &m.MemUsedBytes,
		&m.DiskTotalBytes, &m.DiskUsedBytes,
		&m.Load1, &m.Load5, &m.Load15,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &m, err
}

// ─── Container Metrics ────────────────────────────────────────────────────────

// InsertContainerMetricByDomainPrefix inserts a container metric sample for the
// currently running deployment identified by the project's domain prefix.
// Rows older than 24 hours for the same deployment are pruned on insert.
func (s *Store) InsertContainerMetricByDomainPrefix(ctx context.Context, domainPrefix string, m *ContainerMetric) error {
	// Resolve the running deployment id from domain_prefix.
	var deploymentID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT d.id FROM deployments d
		JOIN projects p ON d.project_id = p.id
		WHERE p.domain_prefix = $1 AND d.status = 'running'
		LIMIT 1
	`, domainPrefix).Scan(&deploymentID)
	if err != nil {
		return err // no running deployment — skip silently upstream
	}
	m.ID = uuid.New()
	m.DeploymentID = deploymentID
	m.CollectedAt = time.Now()
	_, err = s.db.Exec(ctx, `
		INSERT INTO container_metrics
		  (id, deployment_id, collected_at, cpu_percent, mem_usage_bytes, mem_limit_bytes,
		   net_rx_bytes, net_tx_bytes, block_read_bytes, block_write_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, m.ID, m.DeploymentID, m.CollectedAt,
		m.CPUPercent, m.MemUsageBytes, m.MemLimitBytes,
		m.NetRxBytes, m.NetTxBytes, m.BlockReadBytes, m.BlockWriteBytes)
	if err != nil {
		return err
	}
	// Prune samples older than 24 h for this deployment to bound table growth.
	_, _ = s.db.Exec(ctx,
		`DELETE FROM container_metrics WHERE deployment_id=$1 AND collected_at < NOW() - INTERVAL '24 hours'`,
		deploymentID)
	return nil
}

// GetContainerMetricsForProject returns the latest metric samples for the
// currently running deployment of the given project, ordered newest-first.
func (s *Store) GetContainerMetricsForProject(ctx context.Context, projectID uuid.UUID, limit int) ([]*ContainerMetric, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := s.db.Query(ctx, `
		SELECT cm.id, cm.deployment_id, cm.collected_at,
		       cm.cpu_percent, cm.mem_usage_bytes, cm.mem_limit_bytes,
		       cm.net_rx_bytes, cm.net_tx_bytes, cm.block_read_bytes, cm.block_write_bytes
		FROM container_metrics cm
		JOIN deployments d ON cm.deployment_id = d.id
		WHERE d.project_id = $1 AND d.status = 'running'
		ORDER BY cm.collected_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*ContainerMetric, 0)
	for rows.Next() {
		var m ContainerMetric
		if err := rows.Scan(&m.ID, &m.DeploymentID, &m.CollectedAt,
			&m.CPUPercent, &m.MemUsageBytes, &m.MemLimitBytes,
			&m.NetRxBytes, &m.NetTxBytes, &m.BlockReadBytes, &m.BlockWriteBytes); err != nil {
			return nil, err
		}
		items = append(items, &m)
	}
	return items, nil
}

// ─── Project Traffic ──────────────────────────────────────────────────────────

// ResolveProjectIDByDomainPrefix returns the project id for the given
// domain_prefix. Returns (uuid.Nil, nil) when no project matches.
func (s *Store) ResolveProjectIDByDomainPrefix(ctx context.Context, domainPrefix string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM projects WHERE domain_prefix = $1 LIMIT 1`, domainPrefix).Scan(&id)
	if err == pgx.ErrNoRows {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// InsertProjectTraffic records a single HTTP request against a project.
func (s *Store) InsertProjectTraffic(ctx context.Context, t *ProjectTraffic) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.ObservedAt.IsZero() {
		t.ObservedAt = time.Now()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO project_traffic
		  (id, project_id, observed_at, client_ip, host, method, path,
		   status, duration_ms, bytes_sent, user_agent, referer)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, t.ID, t.ProjectID, t.ObservedAt, t.ClientIP, t.Host, t.Method, t.Path,
		t.Status, t.DurationMs, t.BytesSent, t.UserAgent, t.Referer)
	return err
}

// GetProjectTraffic returns the most recent traffic entries for a project,
// newest first. Also prunes rows older than 7 days on each call.
func (s *Store) GetProjectTraffic(ctx context.Context, projectID uuid.UUID, limit int) ([]*ProjectTraffic, error) {
	if limit <= 0 {
		limit = 100
	}
	_, _ = s.db.Exec(ctx,
		`DELETE FROM project_traffic WHERE project_id=$1 AND observed_at < NOW() - INTERVAL '7 days'`,
		projectID)
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, observed_at, client_ip, host, method, path,
		       status, duration_ms, bytes_sent, user_agent, referer
		FROM project_traffic
		WHERE project_id = $1
		ORDER BY observed_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*ProjectTraffic, 0)
	for rows.Next() {
		var t ProjectTraffic
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.ObservedAt,
			&t.ClientIP, &t.Host, &t.Method, &t.Path,
			&t.Status, &t.DurationMs, &t.BytesSent, &t.UserAgent, &t.Referer); err != nil {
			return nil, err
		}
		items = append(items, &t)
	}
	return items, nil
}

// ─── System Settings ──────────────────────────────────────────────────────────

// GetSetting retrieves a single system setting by key.
// Returns an empty string (not an error) if the key is not found.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, key).Scan(&value)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting upserts a system setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO system_settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
	`, key, value)
	return err
}

// GetAllSettings returns all system settings as a map[key]value.
func (s *Store) GetAllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT key, value FROM system_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, nil
}

// SetNodeHealthReport stores the latest health report JSON from an agent node.
func (s *Store) SetNodeHealthReport(ctx context.Context, nodeID uuid.UUID, reportJSON []byte) error {
	_, err := s.db.Exec(ctx,
		`UPDATE nodes SET health_report = $1 WHERE id = $2`,
		reportJSON, nodeID,
	)
	return err
}

// ─── API Tokens ───────────────────────────────────────────────────────────────

func (s *Store) CreateAPIToken(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, name, tokenHash string) (*ApiToken, error) {
	t := &ApiToken{
		ID:        uuid.New(),
		UserID:    userID,
		ProjectID: projectID,
		Name:      name,
		TokenHash: tokenHash,
		CreatedAt: time.Now(),
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO api_tokens (id, user_id, project_id, name, token_hash, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, t.ID, t.UserID, t.ProjectID, t.Name, t.TokenHash, t.CreatedAt)
	return t, err
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*ApiToken, error) {
	var t ApiToken
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, project_id, name, token_hash, last_used_at, created_at FROM api_tokens WHERE token_hash = $1
	`, tokenHash).Scan(&t.ID, &t.UserID, &t.ProjectID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Update last_used_at asynchronously
	_, _ = s.db.Exec(ctx, `UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1`, t.ID)
	return &t, nil
}

func (s *Store) ListAPITokensForProject(ctx context.Context, projectID uuid.UUID) ([]*ApiToken, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, project_id, name, token_hash, last_used_at, created_at FROM api_tokens WHERE project_id = $1 ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]*ApiToken, 0)
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, &t)
	}
	return tokens, nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

// ─── Secrets ─────────────────────────────────────────────────────────────────

// computeSecretPreview derives the non-sensitive display string for a secret:
// - api_key: head 4 + "****" + tail 4 if the value is long enough to safely mask, otherwise fully masked.
// - env_var: full plaintext (values of this type are treated as non-sensitive).
// - password / ssh_key: empty string (values remain write-only).
func computeSecretPreview(secretType SecretType, plaintextValue string) string {
	switch secretType {
	case SecretTypeEnvVar:
		return plaintextValue
	case SecretTypeAPIKey:
		if len(plaintextValue) >= 12 {
			return plaintextValue[:4] + "****" + plaintextValue[len(plaintextValue)-4:]
		}
		return "****"
	default:
		return ""
	}
}

func (s *Store) CreateSecret(ctx context.Context, userID uuid.UUID, name string, secretType SecretType, plaintextValue string) (*Secret, error) {
	if s.encryptionKey == nil {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY is not configured")
	}
	encrypted, err := crypto.Encrypt(s.encryptionKey, plaintextValue)
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}
	sec := &Secret{
		ID:             uuid.New(),
		UserID:         userID,
		Name:           name,
		Type:           secretType,
		EncryptedValue: encrypted,
		ValuePreview:   computeSecretPreview(secretType, plaintextValue),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO secrets (id, user_id, name, type, encrypted_value, value_preview, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, sec.ID, sec.UserID, sec.Name, sec.Type, sec.EncryptedValue, sec.ValuePreview, sec.CreatedAt, sec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sec, nil
}

func (s *Store) ListSecretsForUser(ctx context.Context, userID uuid.UUID) ([]*Secret, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, type, encrypted_value, value_preview, created_at, updated_at
		FROM secrets WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	secrets := make([]*Secret, 0)
	for rows.Next() {
		var sec Secret
		if err := rows.Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.EncryptedValue, &sec.ValuePreview, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		secrets = append(secrets, &sec)
	}
	return secrets, nil
}

func (s *Store) GetSecret(ctx context.Context, id, userID uuid.UUID) (*Secret, error) {
	var sec Secret
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, name, type, encrypted_value, value_preview, created_at, updated_at
		FROM secrets WHERE id = $1 AND user_id = $2
	`, id, userID).Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.EncryptedValue, &sec.ValuePreview, &sec.CreatedAt, &sec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

func (s *Store) DeleteSecret(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM secrets WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

// GetProjectSecretsWithMeta returns secrets bound to a project along with name/type metadata.
// Encrypted values are NOT decrypted — use GetProjectSecretsDecrypted for runtime injection.
func (s *Store) GetProjectSecretsWithMeta(ctx context.Context, projectID uuid.UUID) ([]*ProjectSecretWithMeta, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ps.project_id, ps.secret_id, ps.env_var_name, ps.use_for_git, ps.use_for_build, ps.build_secret_id, ps.git_username,
		       sec.name AS secret_name, sec.type AS secret_type
		FROM project_secrets ps
		JOIN secrets sec ON sec.id = ps.secret_id
		WHERE ps.project_id = $1
		ORDER BY sec.name
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*ProjectSecretWithMeta, 0)
	for rows.Next() {
		var ps ProjectSecretWithMeta
		if err := rows.Scan(&ps.ProjectID, &ps.SecretID, &ps.EnvVarName, &ps.UseForGit, &ps.UseForBuild, &ps.BuildSecretID, &ps.GitUsername, &ps.SecretName, &ps.SecretType); err != nil {
			return nil, err
		}
		result = append(result, &ps)
	}
	return result, nil
}

type DecryptedProjectSecret struct {
	SecretID      uuid.UUID
	SecretName    string
	SecretType    SecretType
	EnvVarName    string
	UseForGit     bool
	UseForBuild   bool
	BuildSecretID string
	GitUsername   string // HTTPS username for git clone (password type with use_for_git=true)
	PlainValue    string
}

// GetProjectSecretsDecrypted returns all secrets for a project with decrypted values.
// Used by the scheduler when building task payloads.
func (s *Store) GetProjectSecretsDecrypted(ctx context.Context, projectID uuid.UUID) ([]*DecryptedProjectSecret, error) {
	if s.encryptionKey == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT ps.secret_id, ps.env_var_name, ps.use_for_git, ps.use_for_build, ps.build_secret_id, ps.git_username,
		       sec.name, sec.type, sec.encrypted_value
		FROM project_secrets ps
		JOIN secrets sec ON sec.id = ps.secret_id
		WHERE ps.project_id = $1
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*DecryptedProjectSecret, 0)
	for rows.Next() {
		var d DecryptedProjectSecret
		var encVal string
		if err := rows.Scan(&d.SecretID, &d.EnvVarName, &d.UseForGit, &d.UseForBuild, &d.BuildSecretID, &d.GitUsername, &d.SecretName, &d.SecretType, &encVal); err != nil {
			return nil, err
		}
		plain, err := crypto.Decrypt(s.encryptionKey, encVal)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %s: %w", d.SecretID, err)
		}
		d.PlainValue = plain
		result = append(result, &d)
	}
	return result, nil
}

// SetProjectSecrets replaces all secret bindings for a project.
func (s *Store) SetProjectSecrets(ctx context.Context, projectID uuid.UUID, bindings []ProjectSecret) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM project_secrets WHERE project_id = $1`, projectID); err != nil {
		return err
	}
	for _, b := range bindings {
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_secrets (project_id, secret_id, env_var_name, use_for_git, use_for_build, build_secret_id, git_username)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, projectID, b.SecretID, b.EnvVarName, b.UseForGit, b.UseForBuild, b.BuildSecretID, b.GitUsername); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ─── Authorization Requests ─────────────────────────────────────────────────

func (s *Store) SetUserAuthorized(ctx context.Context, id uuid.UUID, authorized bool) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET authorized = $1 WHERE id = $2`, authorized, id)
	return err
}

// CreateAuthorizationRequest creates a pending authorization request for the user.
// If a rejected request already exists, it is deleted first so the user can re-request.
func (s *Store) CreateAuthorizationRequest(ctx context.Context, userID uuid.UUID) (*AuthorizationRequest, error) {
	// Remove any prior rejected request so the user can re-request.
	_, _ = s.db.Exec(ctx, `DELETE FROM authorization_requests WHERE user_id = $1 AND status = 'rejected'`, userID)

	var req AuthorizationRequest
	err := s.db.QueryRow(ctx, `
		INSERT INTO authorization_requests (id, user_id, status, created_at, updated_at)
		VALUES ($1, $2, 'pending', NOW(), NOW())
		RETURNING id, user_id, status, reviewed_by, created_at, updated_at
	`, uuid.New(), userID).Scan(&req.ID, &req.UserID, &req.Status, &req.ReviewedBy, &req.CreatedAt, &req.UpdatedAt)
	return &req, err
}

// GetAuthorizationRequestByUser returns the latest authorization request for a user.
func (s *Store) GetAuthorizationRequestByUser(ctx context.Context, userID uuid.UUID) (*AuthorizationRequest, error) {
	var req AuthorizationRequest
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, status, reviewed_by, created_at, updated_at
		FROM authorization_requests WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, userID).Scan(&req.ID, &req.UserID, &req.Status, &req.ReviewedBy, &req.CreatedAt, &req.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &req, err
}

// ListPendingAuthorizationRequests returns all pending requests with user info joined.
func (s *Store) ListPendingAuthorizationRequests(ctx context.Context) ([]*AuthorizationRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ar.id, ar.user_id, ar.status, ar.reviewed_by, ar.created_at, ar.updated_at,
		       u.name, u.email, u.avatar_url
		FROM authorization_requests ar
		JOIN users u ON u.id = ar.user_id
		WHERE ar.status = 'pending'
		ORDER BY ar.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AuthorizationRequest, 0)
	for rows.Next() {
		var req AuthorizationRequest
		if err := rows.Scan(&req.ID, &req.UserID, &req.Status, &req.ReviewedBy,
			&req.CreatedAt, &req.UpdatedAt,
			&req.UserName, &req.UserEmail, &req.UserAvatarURL); err != nil {
			return nil, err
		}
		out = append(out, &req)
	}
	return out, nil
}

// ApproveAuthorizationRequest sets the request to approved and marks the user as authorized.
func (s *Store) ApproveAuthorizationRequest(ctx context.Context, requestID, adminID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE authorization_requests SET status = 'approved', reviewed_by = $1, updated_at = NOW()
		WHERE id = $2 AND status = 'pending'
		RETURNING user_id
	`, adminID, requestID).Scan(&userID)
	if err != nil {
		return fmt.Errorf("request not found or already processed: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET authorized = TRUE WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RejectAuthorizationRequest sets the request to rejected.
func (s *Store) RejectAuthorizationRequest(ctx context.Context, requestID, adminID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE authorization_requests SET status = 'rejected', reviewed_by = $1, updated_at = NOW()
		WHERE id = $2 AND status = 'pending'
	`, adminID, requestID)
	return err
}

// ClearPendingAuthorizationRequests deletes all pending requests.
// Called when the admin disables require_authorization.
func (s *Store) ClearPendingAuthorizationRequests(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM authorization_requests WHERE status = 'pending'`)
	return err
}
