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
	ID         uuid.UUID `db:"id"         json:"id"`
	Email      string    `db:"email"      json:"email"`
	Name       string    `db:"name"       json:"name"`
	AvatarURL  string    `db:"avatar_url" json:"avatar_url"`
	Role       UserRole  `db:"role"       json:"role"`
	Authorized bool      `db:"authorized" json:"authorized"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	// NameOverridden / AvatarOverridden are flipped to TRUE by PATCH /api/me when
	// the user customises the field. UpsertUser then keeps the customised value
	// on subsequent OAuth logins instead of letting the IdP overwrite it.
	NameOverridden   bool `db:"name_overridden"   json:"name_overridden"`
	AvatarOverridden bool `db:"avatar_overridden" json:"avatar_overridden"`
}

// OAuthAccount binds a (provider, provider_user_id) tuple from an external
// identity provider to a local users.id. Created by EnsureUserByOAuth on
// first sign-in for social providers (Discord / Apple / Facebook / Twitter)
// where the IdP may not surface an email.
type OAuthAccount struct {
	Provider       string    `db:"provider"         json:"provider"`
	ProviderUserID string    `db:"provider_user_id" json:"provider_user_id"`
	UserID         uuid.UUID `db:"user_id"          json:"user_id"`
	CreatedAt      time.Time `db:"created_at"       json:"created_at"`
}

// PlatformMember records a user's authorization to use the muvee admin plane
// (the "muvee platform" itself, as distinct from any project deployed on it).
// Subdomain auth users land in `users` for identity but not here, unless they
// also have a platform-side relationship (admin email, project member/owner,
// dataset member/owner).
type PlatformMember struct {
	UserID     uuid.UUID `db:"user_id"    json:"user_id"`
	Role       UserRole  `db:"role"       json:"role"`
	Authorized bool      `db:"authorized" json:"authorized"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

const (
	GitSourceExternal = "external"
	GitSourceHosted   = "hosted"
)

type ProjectType string

const (
	ProjectTypeDeployment ProjectType = "deployment"
	ProjectTypeDomainOnly ProjectType = "domain_only"
	ProjectTypeCompose    ProjectType = "compose"
	ProjectTypeImage      ProjectType = "image"
)

type Project struct {
	ID                 uuid.UUID   `db:"id"                   json:"id"`
	Name               string      `db:"name"                 json:"name"`
	ProjectType        ProjectType `db:"project_type"         json:"project_type"`
	GitURL             string      `db:"git_url"              json:"git_url"`
	GitBranch          string      `db:"git_branch"           json:"git_branch"`
	GitSource          string      `db:"git_source"           json:"git_source"`
	DomainPrefix       string      `db:"domain_prefix"        json:"domain_prefix"`
	DockerfilePath     string      `db:"dockerfile_path"      json:"dockerfile_path"`
	OwnerID            uuid.UUID   `db:"owner_id"             json:"owner_id"`
	AuthRequired       bool        `db:"auth_required"        json:"auth_required"`
	AuthAllowedDomains string      `db:"auth_allowed_domains" json:"auth_allowed_domains"`
	AuthBypassPaths    string      `db:"auth_bypass_paths"    json:"auth_bypass_paths"`
	ContainerPort      int         `db:"container_port"       json:"container_port"`
	MemoryLimit        string      `db:"memory_limit"         json:"memory_limit"`
	VolumeMountPath    string      `db:"volume_mount_path"    json:"volume_mount_path"`
	Description        string      `db:"description"          json:"description"`
	Icon               string      `db:"icon"                 json:"icon"`
	Tags               string      `db:"tags"                 json:"tags"`
	// ComposeFilePath is the path (relative to repo root) of the docker-compose
	// file to deploy. Only used when ProjectType == "compose".
	ComposeFilePath string `db:"compose_file_path" json:"compose_file_path"`
	// ExposeService names the compose service whose port is exposed via Traefik.
	ExposeService string `db:"expose_service"    json:"expose_service"`
	// ExposePort is the container-internal port on ExposeService that should be
	// published as the project's host port.
	ExposePort int `db:"expose_port"       json:"expose_port"`
	// PinnedNodeID locks a compose project to one specific deploy node so its
	// docker named volumes survive across redeploys. Set on first deploy and
	// reused thereafter.
	PinnedNodeID *uuid.UUID `db:"pinned_node_id"    json:"pinned_node_id,omitempty"`
	// ImageRef is the OCI image reference for ProjectType == "image" projects
	// (e.g. "ghcr.io/foo/bar:latest"). Empty for all other project types.
	ImageRef string `db:"image_ref"         json:"image_ref"`
	// AutoDeployEnabled opts the project into automatic redeploy on new commits.
	AutoDeployEnabled bool `db:"auto_deploy_enabled" json:"auto_deploy_enabled"`
	// LastTrackedCommitSHA is the SHA we last triggered a deployment for via the
	// auto-deploy watcher. Compared against the live remote HEAD to decide
	// whether a fresh deployment is needed. Server-managed: never accepted from
	// the API write path.
	LastTrackedCommitSHA string `db:"last_tracked_commit_sha" json:"last_tracked_commit_sha"`
	// LastTrackedImageDigests is a JSON object mapping the literal image string
	// from docker-compose.yml (e.g. "redis:7-alpine") to its last observed
	// digest. Compose-only; server-managed (frozen on API write). Empty object
	// means "not yet seeded" — the watcher records the current digests on first
	// observation without triggering a redeploy.
	LastTrackedImageDigests string `db:"last_tracked_image_digests" json:"last_tracked_image_digests"`
	// AccessMode controls who can reach the deployed downstream service:
	//   "public"  — any authenticated muvee user (legacy behaviour, default)
	//   "private" — only project owner, system admins, and users in project_access_users
	AccessMode string `db:"access_mode"           json:"access_mode"`
	// FixedHostPort + FixedNodeID together pin the project's deployment onto a
	// specific deployer node and force the container's expose port to be
	// published on that exact host port (instead of a Docker-assigned ephemeral).
	// Both nil = dynamic allocation (legacy behaviour). Admin-only writable.
	FixedHostPort *int       `db:"fixed_host_port"      json:"fixed_host_port,omitempty"`
	FixedNodeID   *uuid.UUID `db:"fixed_node_id"        json:"fixed_node_id,omitempty"`
	CreatedAt     time.Time  `db:"created_at"           json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"           json:"updated_at"`
	// EnabledProviders is a comma-separated whitelist of OAuth provider names
	// (e.g. "google,feishu") this project's downstream sign-in flow may use.
	// Empty = inherit the globally-configured set; the SDK and ForwardAuth
	// login pages both honour this column.
	EnabledProviders string `db:"enabled_providers"    json:"enabled_providers"`
	// Branding fields drive the visual of the forward-auth login page served
	// on this project's downstream subdomain. Empty = fall back to the
	// platform-wide system_settings (and then to built-in defaults). See
	// migration 040 for the rationale.
	BrandingSiteName     string `db:"branding_site_name"     json:"branding_site_name"`
	BrandingLogoURL      string `db:"branding_logo_url"      json:"branding_logo_url"`
	BrandingFaviconURL   string `db:"branding_favicon_url"   json:"branding_favicon_url"`
	BrandingPrimaryColor string `db:"branding_primary_color" json:"branding_primary_color"`
	BrandingSidebarBg    string `db:"branding_sidebar_bg"    json:"branding_sidebar_bg"`
	BrandingTagline      string `db:"branding_tagline"       json:"branding_tagline"`
	BrandingDescription  string `db:"branding_description"   json:"branding_description"`
	BrandingFooterText   string `db:"branding_footer_text"   json:"branding_footer_text"`
	BrandingTrustText    string `db:"branding_trust_text"    json:"branding_trust_text"`
	// GitPushURL is computed at API response time for hosted projects; not stored in DB.
	GitPushURL string `db:"-" json:"git_push_url,omitempty"`
	// Owner display fields, populated by ListProjectsForUser / GetProject via LEFT JOIN users.
	OwnerName      string `db:"-" json:"owner_name,omitempty"`
	OwnerEmail     string `db:"-" json:"owner_email,omitempty"`
	OwnerAvatarURL string `db:"-" json:"owner_avatar_url,omitempty"`
}

type MountMode string

const (
	MountModeDependency MountMode = "dependency"
	MountModeReadWrite  MountMode = "readwrite"
)

type Dataset struct {
	ID        uuid.UUID `db:"id"         json:"id"`
	Name      string    `db:"name"       json:"name"`
	NFSPath   string    `db:"nfs_path"   json:"nfs_path"`
	SizeBytes int64     `db:"size_bytes" json:"size_bytes"`
	Checksum  string    `db:"checksum"   json:"checksum"`
	Version   int64     `db:"version"    json:"version"`
	OwnerID   uuid.UUID `db:"owner_id"   json:"owner_id"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

type ProjectDataset struct {
	ProjectID uuid.UUID `db:"project_id" json:"project_id"`
	DatasetID uuid.UUID `db:"dataset_id" json:"dataset_id"`
	MountMode MountMode `db:"mount_mode" json:"mount_mode"`
}

type ProjectMember struct {
	ProjectID uuid.UUID `db:"project_id"`
	UserID    uuid.UUID `db:"user_id"`
}

const (
	ProjectAccessModePublic  = "public"
	ProjectAccessModePrivate = "private"
)

// ProjectAccessUser is an entry in the per-project allow-list consulted by
// Traefik ForwardAuth /verify when the project's access_mode == "private".
// Project owners and system admins are always allowed and do not need a row here.
type ProjectAccessUser struct {
	ProjectID uuid.UUID  `db:"project_id" json:"project_id"`
	UserID    uuid.UUID  `db:"user_id"    json:"user_id"`
	AddedBy   *uuid.UUID `db:"added_by"   json:"added_by,omitempty"`
	AddedAt   time.Time  `db:"added_at"   json:"added_at"`
	// User display fields, populated by ListProjectAccessUsers via LEFT JOIN users.
	Email     string `db:"email"      json:"email"`
	Name      string `db:"name"       json:"name"`
	AvatarURL string `db:"avatar_url" json:"avatar_url"`
}

type DatasetMember struct {
	DatasetID uuid.UUID `db:"dataset_id"`
	UserID    uuid.UUID `db:"user_id"`
}

// ProjectVisit is a per-(project, user) counter of ForwardAuth allow
// decisions, used to give project owners visibility into who has been hitting
// their downstream service. Updated by the in-process visit recorder
// (see internal/api/visit_recorder.go) on every allow decision; not a
// per-request audit log.
type ProjectVisit struct {
	ProjectID    uuid.UUID `db:"project_id"     json:"project_id"`
	UserID       uuid.UUID `db:"user_id"        json:"user_id"`
	FirstSeenAt  time.Time `db:"first_seen_at"  json:"first_seen_at"`
	LastSeenAt   time.Time `db:"last_seen_at"   json:"last_seen_at"`
	VisitCount   int64     `db:"visit_count"    json:"visit_count"`
	// Joined display fields, populated by ListProjectVisits via JOIN users.
	Email     string `db:"email"      json:"email"`
	Name      string `db:"name"       json:"name"`
	AvatarURL string `db:"avatar_url" json:"avatar_url"`
	// InAllowList is TRUE when this user is in project_access_users for the
	// same project. The UI uses this to show / hide a one-click "add to
	// allow-list" button.
	InAllowList bool `db:"in_allow_list" json:"in_allow_list"`
}

type ProjectAccessRequestStatus string

const (
	ProjectAccessRequestPending  ProjectAccessRequestStatus = "pending"
	ProjectAccessRequestApproved ProjectAccessRequestStatus = "approved"
	ProjectAccessRequestDenied   ProjectAccessRequestStatus = "denied"
)

// ProjectAccessRequest is a user's request to be added to a private project's
// allow-list. Created by the user from the /request-access page; decided by
// the project owner (or a platform admin). Approval is a transactional
// INSERT into project_access_users, so the actual grant lives in one place.
type ProjectAccessRequest struct {
	ID           uuid.UUID                  `db:"id"            json:"id"`
	ProjectID    uuid.UUID                  `db:"project_id"    json:"project_id"`
	UserID       uuid.UUID                  `db:"user_id"       json:"user_id"`
	Reason       string                     `db:"reason"        json:"reason"`
	Status       ProjectAccessRequestStatus `db:"status"        json:"status"`
	RequestedAt  time.Time                  `db:"requested_at"  json:"requested_at"`
	DecidedAt    *time.Time                 `db:"decided_at"    json:"decided_at,omitempty"`
	DecidedBy    *uuid.UUID                 `db:"decided_by"    json:"decided_by,omitempty"`
	// Joined user display fields, populated by list helpers via JOIN users.
	UserEmail     string `db:"user_email"      json:"user_email,omitempty"`
	UserName      string `db:"user_name"       json:"user_name,omitempty"`
	UserAvatarURL string `db:"user_avatar_url" json:"user_avatar_url,omitempty"`
	// Joined project display fields, populated by ListPendingRequestsForOwner
	// (the owner-dashboard view, where requests for several projects are mixed).
	ProjectName         string `db:"project_name"          json:"project_name,omitempty"`
	ProjectDomainPrefix string `db:"project_domain_prefix" json:"project_domain_prefix,omitempty"`
}

type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusBuilding  DeploymentStatus = "building"
	DeploymentStatusDeploying DeploymentStatus = "deploying"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusFailed    DeploymentStatus = "failed"
	DeploymentStatusStopped   DeploymentStatus = "stopped"
)

type Deployment struct {
	ID           uuid.UUID        `db:"id"            json:"id"`
	ProjectID    uuid.UUID        `db:"project_id"    json:"project_id"`
	ImageTag     string           `db:"image_tag"     json:"image_tag"`
	CommitSHA    string           `db:"commit_sha"    json:"commit_sha"`
	Status       DeploymentStatus `db:"status"        json:"status"`
	NodeID       *uuid.UUID       `db:"node_id"       json:"node_id"`
	HostPort     int              `db:"host_port"     json:"host_port"`
	Logs         string           `db:"logs"          json:"logs"`
	RestartCount int              `db:"restart_count" json:"restart_count"`
	OOMKilled    bool             `db:"oom_killed"    json:"oom_killed"`
	CreatedAt    time.Time        `db:"created_at"    json:"created_at"`
	UpdatedAt    time.Time        `db:"updated_at"    json:"updated_at"`
}

// PublicProjectInfo is a minimal view of a running project exposed to unauthenticated users.
type PublicProjectInfo struct {
	ID             uuid.UUID `db:"id"              json:"id"`
	Name           string    `db:"name"            json:"name"`
	DomainPrefix   string    `db:"domain_prefix"   json:"domain_prefix"`
	Description    string    `db:"description"     json:"description"`
	Icon           string    `db:"icon"            json:"icon"`
	Tags           string    `db:"tags"            json:"tags"`
	AuthRequired   bool      `db:"auth_required"   json:"auth_required"`
	AccessMode     string    `db:"access_mode"     json:"access_mode"`
	OwnerName      string    `db:"owner_name"      json:"owner_name"`
	OwnerAvatarURL string    `db:"owner_avatar_url" json:"owner_avatar_url"`
	UpdatedAt      time.Time `db:"updated_at"      json:"updated_at"`
}

// RunningDeploymentInfo is a denormalized view of a running deployment
// used to generate Traefik HTTP provider configuration.
type RunningDeploymentInfo struct {
	DeploymentID       uuid.UUID  `db:"deployment_id"`
	ProjectID          uuid.UUID  `db:"project_id"`
	DomainPrefix       string     `db:"domain_prefix"`
	AuthRequired       bool       `db:"auth_required"`
	AuthAllowedDomains string     `db:"auth_allowed_domains"`
	AuthBypassPaths    string     `db:"auth_bypass_paths"`
	AccessMode         string     `db:"access_mode"`
	HostIP             string     `db:"host_ip"`
	HostPort           int        `db:"host_port"`
	NodeID             *uuid.UUID `db:"node_id"`
}

type NodeRole string

const (
	NodeRoleBuilder NodeRole = "builder"
	NodeRoleDeploy  NodeRole = "deploy"
)

type Node struct {
	ID               uuid.UUID `db:"id"                 json:"id"`
	Hostname         string    `db:"hostname"           json:"hostname"`
	Role             NodeRole  `db:"role"               json:"role"`
	HostIP           string    `db:"host_ip"            json:"host_ip"`
	MaxStorageBytes  int64     `db:"max_storage_bytes"  json:"max_storage_bytes"`
	UsedStorageBytes int64     `db:"used_storage_bytes" json:"used_storage_bytes"`
	LastSeenAt       time.Time `db:"last_seen_at"       json:"last_seen_at"`
	CreatedAt        time.Time `db:"created_at"         json:"created_at"`
	// HealthReport is the latest self-reported health status from the agent.
	// Stored as raw JSON (can be nil if no report received yet).
	HealthReport []byte `db:"health_report" json:"health_report,omitempty"`
}

type NodeDataset struct {
	NodeID     uuid.UUID `db:"node_id"`
	DatasetID  uuid.UUID `db:"dataset_id"`
	LastUsedAt time.Time `db:"last_used_at"`
	SizeBytes  int64     `db:"size_bytes"`
}

type DatasetSnapshot struct {
	ID             uuid.UUID `db:"id"              json:"id"`
	DatasetID      uuid.UUID `db:"dataset_id"      json:"dataset_id"`
	ScannedAt      time.Time `db:"scanned_at"      json:"scanned_at"`
	TotalFiles     int64     `db:"total_files"     json:"total_files"`
	TotalSizeBytes int64     `db:"total_size_bytes" json:"total_size_bytes"`
	Version        int64     `db:"version"         json:"version"`
}

// SystemSetting is a single key-value pair in the system_settings table.
type SystemSetting struct {
	Key       string    `db:"key"        json:"key"`
	Value     string    `db:"value"      json:"value"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

type AuthorizationRequestStatus string

const (
	AuthRequestPending  AuthorizationRequestStatus = "pending"
	AuthRequestApproved AuthorizationRequestStatus = "approved"
	AuthRequestRejected AuthorizationRequestStatus = "rejected"
)

// AccessMode determines who is allowed to sign in and reach an authorized state.
// Stored in system_settings under the "access_mode" key.
type AccessMode string

const (
	// AccessModeOpen — any OAuth user passing the domain/org check is auto-authorized.
	AccessModeOpen AccessMode = "open"
	// AccessModeInvite — only emails on the invitations white-list (or holders
	// of a valid invitation_link token) may sign in. Others are rejected.
	AccessModeInvite AccessMode = "invite"
	// AccessModeRequest — anyone passing the OAuth/domain check is created with
	// authorized=FALSE and must request access via authorization_requests.
	AccessModeRequest AccessMode = "request"
)

// Invitation is an email white-list entry consulted in AccessModeInvite.
type Invitation struct {
	ID        uuid.UUID  `db:"id"          json:"id"`
	Email     string     `db:"email"       json:"email"`
	InvitedBy *uuid.UUID `db:"invited_by"  json:"invited_by,omitempty"`
	CreatedAt time.Time  `db:"created_at"  json:"created_at"`
	// Joined display fields, populated by ListInvitations.
	InvitedByName  string `db:"-" json:"invited_by_name,omitempty"`
	InvitedByEmail string `db:"-" json:"invited_by_email,omitempty"`
}

// InvitationLink is an access token that auto-authorizes a logging-in user.
//
// Two flavours share this row:
//   - Platform invite  (ProjectID == nil): single-use unless MaxUses overrides
//     it; consumption flips UsedAt/UsedBy. Used in AccessModeInvite.
//   - Project invite   (ProjectID != nil): consumption is recorded in the
//     invitation_link_uses table; the link stays valid until UseCount >=
//     MaxUses (when MaxUses != nil) or ExpiresAt passes, or it is revoked.
//     Each consumer is also upserted into project_access_users for ProjectID.
type InvitationLink struct {
	ID        uuid.UUID  `db:"id"          json:"id"`
	TokenHash string     `db:"token_hash"  json:"-"`
	InvitedBy *uuid.UUID `db:"invited_by"  json:"invited_by,omitempty"`
	ProjectID *uuid.UUID `db:"project_id"  json:"project_id,omitempty"`
	MaxUses   *int       `db:"max_uses"    json:"max_uses,omitempty"`
	ExpiresAt *time.Time `db:"expires_at"  json:"expires_at,omitempty"`
	UsedAt    *time.Time `db:"used_at"     json:"used_at,omitempty"`
	UsedBy    *uuid.UUID `db:"used_by"     json:"used_by,omitempty"`
	CreatedAt time.Time  `db:"created_at"  json:"created_at"`
	// Token is only populated when freshly created (one-time return) — never stored.
	Token string `db:"-" json:"token,omitempty"`
	// Joined display fields, populated by listing queries.
	InvitedByName  string `db:"-" json:"invited_by_name,omitempty"`
	InvitedByEmail string `db:"-" json:"invited_by_email,omitempty"`
	UsedByEmail    string `db:"-" json:"used_by_email,omitempty"`
	// UseCount is populated for project invites by ListProjectInvitationLinks.
	UseCount int `db:"-" json:"use_count,omitempty"`
}

// InvitationLinkUse records one consumption of a multi-use project invitation
// link by a specific user.
type InvitationLinkUse struct {
	ID        uuid.UUID `db:"id"        json:"id"`
	LinkID    uuid.UUID `db:"link_id"   json:"link_id"`
	UserID    uuid.UUID `db:"user_id"   json:"user_id"`
	UsedAt    time.Time `db:"used_at"   json:"used_at"`
	UserEmail string    `db:"-"         json:"user_email,omitempty"`
	UserName  string    `db:"-"         json:"user_name,omitempty"`
	AvatarURL string    `db:"-"         json:"avatar_url,omitempty"`
}

// AuthorizationRequest tracks a user's request to be authorized on the platform.
type AuthorizationRequest struct {
	ID         uuid.UUID                  `db:"id"          json:"id"`
	UserID     uuid.UUID                  `db:"user_id"     json:"user_id"`
	Status     AuthorizationRequestStatus `db:"status"      json:"status"`
	ReviewedBy *uuid.UUID                 `db:"reviewed_by" json:"reviewed_by"`
	CreatedAt  time.Time                  `db:"created_at"  json:"created_at"`
	UpdatedAt  time.Time                  `db:"updated_at"  json:"updated_at"`
	// Joined fields (not always populated)
	UserName      string `db:"-" json:"user_name,omitempty"`
	UserEmail     string `db:"-" json:"user_email,omitempty"`
	UserAvatarURL string `db:"-" json:"user_avatar_url,omitempty"`
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
	ProjectID  *uuid.UUID `db:"project_id"`
	Name       string     `db:"name"`
	TokenHash  string     `db:"token_hash"`
	LastUsedAt *time.Time `db:"last_used_at"`
	ExpiresAt  *time.Time `db:"expires_at"`
	CreatedAt  time.Time  `db:"created_at"`
	// Token is only populated when freshly created (never stored)
	Token string `db:"-"`
}

type DatasetFileHistory struct {
	ID          uuid.UUID     `db:"id"           json:"id"`
	DatasetID   uuid.UUID     `db:"dataset_id"   json:"dataset_id"`
	FilePath    string        `db:"file_path"    json:"file_path"`
	EventType   FileEventType `db:"event_type"   json:"event_type"`
	OldSize     int64         `db:"old_size"     json:"old_size"`
	NewSize     int64         `db:"new_size"     json:"new_size"`
	OldChecksum string        `db:"old_checksum" json:"old_checksum"`
	NewChecksum string        `db:"new_checksum" json:"new_checksum"`
	SnapshotID  uuid.UUID     `db:"snapshot_id"  json:"snapshot_id"`
	OccurredAt  time.Time     `db:"occurred_at"  json:"occurred_at"`
}

type TaskType string

const (
	TaskTypeBuild       TaskType = "build"
	TaskTypeDeploy      TaskType = "deploy"
	TaskTypeCleanup     TaskType = "cleanup"
	TaskTypeRuntimeLogs TaskType = "runtime_logs"
	TaskTypeRestart     TaskType = "restart"
	TaskTypeEnv         TaskType = "env"
	TaskTypeDescribe    TaskType = "describe"
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
	// SecretTypeAPIKey stores an API key / token. The full value is encrypted;
	// a masked preview (head4 + **** + tail4) is stored alongside for UI display.
	SecretTypeAPIKey SecretType = "api_key"
	// SecretTypeEnvVar stores a non-sensitive environment variable value.
	// The full plaintext is stored as the value preview and shown in the UI.
	SecretTypeEnvVar SecretType = "env_var"
)

type Secret struct {
	ID             uuid.UUID  `db:"id"`
	UserID         uuid.UUID  `db:"user_id"`
	Name           string     `db:"name"`
	Type           SecretType `db:"type"`
	EncryptedValue string     `db:"encrypted_value"`
	// ValuePreview is a non-sensitive display string: masked fingerprint for api_key,
	// full plaintext for env_var, and empty for password / ssh_key.
	ValuePreview string    `db:"value_preview"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

type ProjectSecret struct {
	ProjectID  uuid.UUID `db:"project_id"`
	SecretID   uuid.UUID `db:"secret_id"`
	EnvVarName string    `db:"env_var_name"`
	UseForGit  bool      `db:"use_for_git"`
	// UseForBuild controls whether this secret is passed to docker buildx as a build secret.
	UseForBuild bool `db:"use_for_build"`
	// BuildSecretID is exposed in Dockerfile as /run/secrets/<build_secret_id>.
	BuildSecretID string `db:"build_secret_id"`
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

// NodeMetric is a single point-in-time sample of host-level resource usage
// collected by the deploy/builder agent via /proc files and df.
type NodeMetric struct {
	ID             uuid.UUID `db:"id"              json:"id"`
	NodeID         uuid.UUID `db:"node_id"         json:"node_id"`
	CollectedAt    time.Time `db:"collected_at"    json:"collected_at"`
	CPUPercent     float64   `db:"cpu_percent"     json:"cpu_percent"`
	MemTotalBytes  int64     `db:"mem_total_bytes" json:"mem_total_bytes"`
	MemUsedBytes   int64     `db:"mem_used_bytes"  json:"mem_used_bytes"`
	DiskTotalBytes int64     `db:"disk_total_bytes" json:"disk_total_bytes"`
	DiskUsedBytes  int64     `db:"disk_used_bytes" json:"disk_used_bytes"`
	Load1          float64   `db:"load1"           json:"load1"`
	Load5          float64   `db:"load5"           json:"load5"`
	Load15         float64   `db:"load15"          json:"load15"`
}

// ProjectTraffic is a single HTTP request observed by Traefik and attributed
// to a project via its domain_prefix.
type ProjectTraffic struct {
	ID         uuid.UUID `db:"id"           json:"id"`
	ProjectID  uuid.UUID `db:"project_id"   json:"project_id"`
	ObservedAt time.Time `db:"observed_at"  json:"observed_at"`
	ClientIP   string    `db:"client_ip"    json:"client_ip"`
	Host       string    `db:"host"         json:"host"`
	Method     string    `db:"method"       json:"method"`
	Path       string    `db:"path"         json:"path"`
	Status     int       `db:"status"       json:"status"`
	DurationMs int64     `db:"duration_ms"  json:"duration_ms"`
	BytesSent  int64     `db:"bytes_sent"   json:"bytes_sent"`
	UserAgent  string    `db:"user_agent"   json:"user_agent"`
	Referer    string    `db:"referer"      json:"referer"`
}

// ProjectAlias is an additional host name that routes to the same project as
// the project's `<domain_prefix>.<base_domain>`. Stored lowercased; uniqueness
// across all projects is enforced by the DB.
type ProjectAlias struct {
	ID        uuid.UUID `db:"id"         json:"id"`
	ProjectID uuid.UUID `db:"project_id" json:"project_id"`
	Host      string    `db:"host"       json:"host"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// ContainerMetric is a single point-in-time sample of container resource usage
// collected by the deploy agent via `docker stats --no-stream`.
type ContainerMetric struct {
	ID              uuid.UUID `db:"id"               json:"id"`
	DeploymentID    uuid.UUID `db:"deployment_id"    json:"deployment_id"`
	CollectedAt     time.Time `db:"collected_at"     json:"collected_at"`
	CPUPercent      float64   `db:"cpu_percent"      json:"cpu_percent"`
	MemUsageBytes   int64     `db:"mem_usage_bytes"  json:"mem_usage_bytes"`
	MemLimitBytes   int64     `db:"mem_limit_bytes"  json:"mem_limit_bytes"`
	NetRxBytes      int64     `db:"net_rx_bytes"     json:"net_rx_bytes"`
	NetTxBytes      int64     `db:"net_tx_bytes"     json:"net_tx_bytes"`
	BlockReadBytes  int64     `db:"block_read_bytes" json:"block_read_bytes"`
	BlockWriteBytes int64     `db:"block_write_bytes" json:"block_write_bytes"`
}
