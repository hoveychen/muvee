package store

import (
	"time"

	"github.com/google/uuid"
)

type UserRole string

const (
	UserRoleAdmin  UserRole = "admin"
	UserRoleMember UserRole = "member"
)

type User struct {
	ID        uuid.UUID `db:"id"`
	Email     string    `db:"email"`
	Name      string    `db:"name"`
	AvatarURL string    `db:"avatar_url"`
	Role      UserRole  `db:"role"`
	CreatedAt time.Time `db:"created_at"`
}

type Project struct {
	ID                 uuid.UUID `db:"id"`
	Name               string    `db:"name"`
	GitURL             string    `db:"git_url"`
	GitBranch          string    `db:"git_branch"`
	DomainPrefix       string    `db:"domain_prefix"`
	DockerfilePath     string    `db:"dockerfile_path"`
	OwnerID            uuid.UUID `db:"owner_id"`
	AuthRequired       bool      `db:"auth_required"`
	AuthAllowedDomains string    `db:"auth_allowed_domains"`
	ContainerPort      int       `db:"container_port"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

type MountMode string

const (
	MountModeDependency MountMode = "dependency"
	MountModeReadWrite  MountMode = "readwrite"
)

type Dataset struct {
	ID        uuid.UUID `db:"id"`
	Name      string    `db:"name"`
	NFSPath   string    `db:"nfs_path"`
	SizeBytes int64     `db:"size_bytes"`
	Checksum  string    `db:"checksum"`
	Version   int64     `db:"version"`
	OwnerID   uuid.UUID `db:"owner_id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

type ProjectDataset struct {
	ProjectID uuid.UUID `db:"project_id"`
	DatasetID uuid.UUID `db:"dataset_id"`
	MountMode MountMode `db:"mount_mode"`
}

type ProjectMember struct {
	ProjectID uuid.UUID `db:"project_id"`
	UserID    uuid.UUID `db:"user_id"`
}

type DatasetMember struct {
	DatasetID uuid.UUID `db:"dataset_id"`
	UserID    uuid.UUID `db:"user_id"`
}

type DeploymentStatus string

const (
	DeploymentStatusPending  DeploymentStatus = "pending"
	DeploymentStatusBuilding DeploymentStatus = "building"
	DeploymentStatusDeploying DeploymentStatus = "deploying"
	DeploymentStatusRunning  DeploymentStatus = "running"
	DeploymentStatusFailed   DeploymentStatus = "failed"
	DeploymentStatusStopped  DeploymentStatus = "stopped"
)

type Deployment struct {
	ID        uuid.UUID        `db:"id"`
	ProjectID uuid.UUID        `db:"project_id"`
	ImageTag  string           `db:"image_tag"`
	CommitSHA string           `db:"commit_sha"`
	Status    DeploymentStatus `db:"status"`
	NodeID    *uuid.UUID       `db:"node_id"`
	HostPort  int              `db:"host_port"`
	Logs      string           `db:"logs"`
	CreatedAt time.Time        `db:"created_at"`
	UpdatedAt time.Time        `db:"updated_at"`
}

// RunningDeploymentInfo is a denormalized view of a running deployment
// used to generate Traefik HTTP provider configuration.
type RunningDeploymentInfo struct {
	DeploymentID       uuid.UUID `db:"deployment_id"`
	ProjectID          uuid.UUID `db:"project_id"`
	DomainPrefix       string    `db:"domain_prefix"`
	AuthRequired       bool      `db:"auth_required"`
	AuthAllowedDomains string    `db:"auth_allowed_domains"`
	HostIP             string    `db:"host_ip"`
	HostPort           int       `db:"host_port"`
}

type NodeRole string

const (
	NodeRoleBuilder NodeRole = "builder"
	NodeRoleDeploy  NodeRole = "deploy"
)

type Node struct {
	ID               uuid.UUID `db:"id"`
	Hostname         string    `db:"hostname"`
	Role             NodeRole  `db:"role"`
	HostIP           string    `db:"host_ip"`
	MaxStorageBytes  int64     `db:"max_storage_bytes"`
	UsedStorageBytes int64     `db:"used_storage_bytes"`
	LastSeenAt       time.Time `db:"last_seen_at"`
	CreatedAt        time.Time `db:"created_at"`
}

type NodeDataset struct {
	NodeID     uuid.UUID `db:"node_id"`
	DatasetID  uuid.UUID `db:"dataset_id"`
	LastUsedAt time.Time `db:"last_used_at"`
	SizeBytes  int64     `db:"size_bytes"`
}

type DatasetSnapshot struct {
	ID             uuid.UUID `db:"id"`
	DatasetID      uuid.UUID `db:"dataset_id"`
	ScannedAt      time.Time `db:"scanned_at"`
	TotalFiles     int64     `db:"total_files"`
	TotalSizeBytes int64     `db:"total_size_bytes"`
	Version        int64     `db:"version"`
}

type FileEventType string

const (
	FileEventAdded    FileEventType = "added"
	FileEventModified FileEventType = "modified"
	FileEventDeleted  FileEventType = "deleted"
)

type ApiToken struct {
	ID         uuid.UUID  `db:"id"`
	UserID     uuid.UUID  `db:"user_id"`
	Name       string     `db:"name"`
	TokenHash  string     `db:"token_hash"`
	LastUsedAt *time.Time `db:"last_used_at"`
	CreatedAt  time.Time  `db:"created_at"`
	// Token is only populated when freshly created (never stored)
	Token string `db:"-"`
}

type DatasetFileHistory struct {
	ID           uuid.UUID     `db:"id"`
	DatasetID    uuid.UUID     `db:"dataset_id"`
	FilePath     string        `db:"file_path"`
	EventType    FileEventType `db:"event_type"`
	OldSize      int64         `db:"old_size"`
	NewSize      int64         `db:"new_size"`
	OldChecksum  string        `db:"old_checksum"`
	NewChecksum  string        `db:"new_checksum"`
	SnapshotID   uuid.UUID     `db:"snapshot_id"`
	OccurredAt   time.Time     `db:"occurred_at"`
}

type TaskType string

const (
	TaskTypeBuild  TaskType = "build"
	TaskTypeDeploy TaskType = "deploy"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

type SecretType string

const (
	SecretTypePassword SecretType = "password"
	SecretTypeSSHKey   SecretType = "ssh_key"
)

type Secret struct {
	ID             uuid.UUID  `db:"id"`
	UserID         uuid.UUID  `db:"user_id"`
	Name           string     `db:"name"`
	Type           SecretType `db:"type"`
	EncryptedValue string     `db:"encrypted_value"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

type ProjectSecret struct {
	ProjectID   uuid.UUID `db:"project_id"`
	SecretID    uuid.UUID `db:"secret_id"`
	EnvVarName  string    `db:"env_var_name"`
	UseForGit   bool      `db:"use_for_git"`
	// GitUsername is the HTTPS username used when UseForGit=true and the secret type is password.
	// The builder rewrites the git URL as https://GitUsername:SECRET@host/...
	// For GitHub fine-grained PATs, use "x-access-token" or "oauth2".
	GitUsername string `db:"git_username"`
}

// ProjectSecretWithMeta is a ProjectSecret enriched with the Secret's name and type,
// used when listing secrets bound to a project.
type ProjectSecretWithMeta struct {
	ProjectSecret
	SecretName string     `db:"secret_name"`
	SecretType SecretType `db:"secret_type"`
}

type Task struct {
	ID           uuid.UUID              `db:"id"`
	Type         TaskType               `db:"type"`
	NodeID       *uuid.UUID             `db:"node_id"`
	DeploymentID uuid.UUID              `db:"deployment_id"`
	Payload      map[string]interface{} `db:"-"`
	PayloadJSON  string                 `db:"payload"`
	Status       TaskStatus             `db:"status"`
	Result       string                 `db:"result"`
	CreatedAt    time.Time              `db:"created_at"`
	UpdatedAt    time.Time              `db:"updated_at"`
}
