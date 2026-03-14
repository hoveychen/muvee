package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
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
		RETURNING id, email, name, avatar_url, role, created_at
	`, uuid.New(), email, name, avatarURL).Scan(
		&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.CreatedAt,
	)
	return &u, err
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, avatar_url, role, created_at FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.Query(ctx, `SELECT id, email, name, avatar_url, role, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.CreatedAt); err != nil {
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

func (s *Store) CreateProject(ctx context.Context, p *Project) (*Project, error) {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	if p.ContainerPort == 0 {
		p.ContainerPort = 8080
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO projects (id, name, git_url, git_branch, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, container_port, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, p.ID, p.Name, p.GitURL, p.GitBranch, p.DomainPrefix, p.DockerfilePath, p.OwnerID, p.AuthRequired, p.AuthAllowedDomains, p.ContainerPort, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO project_members (project_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p.ID, p.OwnerID)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (*Project, error) {
	var p Project
	err := s.db.QueryRow(ctx, `
		SELECT id, name, git_url, git_branch, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, container_port, created_at, updated_at
		FROM projects WHERE id = $1
	`, id).Scan(&p.ID, &p.Name, &p.GitURL, &p.GitBranch, &p.DomainPrefix, &p.DockerfilePath, &p.OwnerID, &p.AuthRequired, &p.AuthAllowedDomains, &p.ContainerPort, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

func (s *Store) ListProjectsForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]*Project, error) {
	var query string
	var args []interface{}
	if isAdmin {
		query = `SELECT id, name, git_url, git_branch, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, container_port, created_at, updated_at FROM projects ORDER BY created_at DESC`
	} else {
		query = `SELECT p.id, p.name, p.git_url, p.git_branch, p.domain_prefix, p.dockerfile_path, p.owner_id, p.auth_required, p.auth_allowed_domains, p.container_port, p.created_at, p.updated_at
			FROM projects p JOIN project_members pm ON p.id = pm.project_id WHERE pm.user_id = $1 ORDER BY p.created_at DESC`
		args = []interface{}{userID}
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []*Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.GitURL, &p.GitBranch, &p.DomainPrefix, &p.DockerfilePath, &p.OwnerID, &p.AuthRequired, &p.AuthAllowedDomains, &p.ContainerPort, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, &p)
	}
	return projects, nil
}

func (s *Store) UpdateProject(ctx context.Context, p *Project) error {
	p.UpdatedAt = time.Now()
	if p.ContainerPort == 0 {
		p.ContainerPort = 8080
	}
	_, err := s.db.Exec(ctx, `
		UPDATE projects SET name=$1, git_url=$2, git_branch=$3, domain_prefix=$4, dockerfile_path=$5, auth_required=$6, auth_allowed_domains=$7, container_port=$8, updated_at=$9 WHERE id=$10
	`, p.Name, p.GitURL, p.GitBranch, p.DomainPrefix, p.DockerfilePath, p.AuthRequired, p.AuthAllowedDomains, p.ContainerPort, p.UpdatedAt, p.ID)
	return err
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
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
	var items []ProjectDataset
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
	var datasets []*Dataset
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
		INSERT INTO deployments (id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, d.ID, d.ProjectID, d.ImageTag, d.CommitSHA, d.Status, d.NodeID, d.HostPort, d.Logs, d.CreatedAt, d.UpdatedAt)
	return d, err
}

func (s *Store) GetDeployment(ctx context.Context, id uuid.UUID) (*Deployment, error) {
	var d Deployment
	err := s.db.QueryRow(ctx, `
		SELECT id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, created_at, updated_at FROM deployments WHERE id = $1
	`, id).Scan(&d.ID, &d.ProjectID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.NodeID, &d.HostPort, &d.Logs, &d.CreatedAt, &d.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &d, err
}

func (s *Store) ListDeployments(ctx context.Context, projectID uuid.UUID) ([]*Deployment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, image_tag, commit_sha, status, node_id, host_port, logs, created_at, updated_at FROM deployments WHERE project_id = $1 ORDER BY created_at DESC LIMIT 50
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deployments []*Deployment
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ImageTag, &d.CommitSHA, &d.Status, &d.NodeID, &d.HostPort, &d.Logs, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		deployments = append(deployments, &d)
	}
	return deployments, nil
}

// SetDeploymentHostPort stores the host port after a successful deploy and marks status as running.
func (s *Store) SetDeploymentHostPort(ctx context.Context, id uuid.UUID, hostPort int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments SET host_port=$1, status='running', updated_at=NOW() WHERE id=$2
	`, hostPort, id)
	return err
}

// StopProjectDeployments marks all running deployments for a project as stopped, except the given one.
func (s *Store) StopProjectDeployments(ctx context.Context, projectID, exceptID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments SET status='stopped', updated_at=NOW()
		WHERE project_id=$1 AND id != $2 AND status='running'
	`, projectID, exceptID)
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
		SELECT d.id, d.project_id, p.domain_prefix, p.auth_required, p.auth_allowed_domains, n.host_ip, d.host_port
		FROM deployments d
		JOIN projects p ON d.project_id = p.id
		JOIN nodes n ON d.node_id = n.id
		WHERE d.status = 'running' AND d.host_port > 0 AND n.host_ip != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*RunningDeploymentInfo
	for rows.Next() {
		var r RunningDeploymentInfo
		if err := rows.Scan(&r.DeploymentID, &r.ProjectID, &r.DomainPrefix, &r.AuthRequired, &r.AuthAllowedDomains, &r.HostIP, &r.HostPort); err != nil {
			return nil, err
		}
		items = append(items, &r)
	}
	return items, nil
}

func (s *Store) UpdateDeploymentStatus(ctx context.Context, id uuid.UUID, status DeploymentStatus, logs string) error {
	_, err := s.db.Exec(ctx, `UPDATE deployments SET status=$1, logs=$2, updated_at=NOW() WHERE id=$3`, status, logs, id)
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
	err := s.db.QueryRow(ctx, `SELECT id FROM nodes WHERE hostname = $1`, n.Hostname).Scan(&existing.ID)
	if err == pgx.ErrNoRows {
		n.ID = uuid.New()
		n.CreatedAt = time.Now()
		_, err = s.db.Exec(ctx, `
			INSERT INTO nodes (id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, n.ID, n.Hostname, n.Role, n.HostIP, n.MaxStorageBytes, n.UsedStorageBytes, n.LastSeenAt, n.CreatedAt)
	} else if err == nil {
		n.ID = existing.ID
		_, err = s.db.Exec(ctx, `UPDATE nodes SET role=$1, host_ip=$2, max_storage_bytes=$3, last_seen_at=$4 WHERE id=$5`,
			n.Role, n.HostIP, n.MaxStorageBytes, n.LastSeenAt, n.ID)
	}
	return n, err
}

func (s *Store) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.Query(ctx, `SELECT id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at FROM nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []*Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Hostname, &n.Role, &n.HostIP, &n.MaxStorageBytes, &n.UsedStorageBytes, &n.LastSeenAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func (s *Store) GetDeployNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.Query(ctx, `SELECT id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at FROM nodes WHERE role = 'deploy' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []*Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Hostname, &n.Role, &n.HostIP, &n.MaxStorageBytes, &n.UsedStorageBytes, &n.LastSeenAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

// ─── Node Datasets ───────────────────────────────────────────────────────────

func (s *Store) GetNodeDatasets(ctx context.Context, nodeID uuid.UUID) ([]*NodeDataset, error) {
	rows, err := s.db.Query(ctx, `SELECT node_id, dataset_id, last_used_at, size_bytes FROM node_datasets WHERE node_id = $1`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*NodeDataset
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
	var items []*NodeDataset
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
	var tasks []*Task
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
	var snaps []*DatasetSnapshot
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
	var items []*DatasetFileHistory
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

// ─── API Tokens ───────────────────────────────────────────────────────────────

func (s *Store) CreateAPIToken(ctx context.Context, userID uuid.UUID, name, tokenHash string) (*ApiToken, error) {
	t := &ApiToken{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      name,
		TokenHash: tokenHash,
		CreatedAt: time.Now(),
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO api_tokens (id, user_id, name, token_hash, created_at)
		VALUES ($1,$2,$3,$4,$5)
	`, t.ID, t.UserID, t.Name, t.TokenHash, t.CreatedAt)
	return t, err
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*ApiToken, error) {
	var t ApiToken
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, name, token_hash, last_used_at, created_at FROM api_tokens WHERE token_hash = $1
	`, tokenHash).Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.CreatedAt)
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

func (s *Store) ListAPITokensForUser(ctx context.Context, userID uuid.UUID) ([]*ApiToken, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, token_hash, last_used_at, created_at FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []*ApiToken
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.CreatedAt); err != nil {
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
