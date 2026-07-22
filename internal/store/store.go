package store

import (
	"context"
	"encoding/json"
	"errors"
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

// userColumns is the canonical SELECT list for users. Keep it in lockstep with
// scanUser so all User reads pick up new columns at once.
// users.email is nullable since migration 039 to accommodate social-login
// users that do not surface an email (Discord, Twitter free tier, Apple
// Hide-My-Email never-shared). COALESCE keeps the scan target as plain
// string -- callers see "" for NULL emails.
const userColumns = `id, COALESCE(email, '') AS email, name, avatar_url, role, authorized, created_at, name_overridden, avatar_overridden`

func scanUser(scanner interface {
	Scan(dest ...interface{}) error
}, u *User) error {
	return scanner.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.Authorized, &u.CreatedAt, &u.NameOverridden, &u.AvatarOverridden)
}

// UpsertUser inserts a new user or updates the existing one keyed by email.
// authorizedOnInsert sets the `authorized` flag for newly created rows; for
// existing rows the flag is preserved (callers wanting to flip it should use
// SetUserAuthorized after the upsert). The second return value reports whether
// the row was newly inserted (true) vs. already present (false).
func (s *Store) UpsertUser(ctx context.Context, email, name, avatarURL string, authorizedOnInsert bool) (*User, bool, error) {
	var u User
	var inserted bool
	err := s.db.QueryRow(ctx, `
		INSERT INTO users (id, email, name, avatar_url, role, authorized, created_at)
		VALUES ($1, $2, $3, $4, 'member', $5, NOW())
		ON CONFLICT (email) DO UPDATE SET
			name       = CASE WHEN users.name_overridden   THEN users.name       ELSE EXCLUDED.name       END,
			avatar_url = CASE WHEN users.avatar_overridden THEN users.avatar_url ELSE EXCLUDED.avatar_url END
		RETURNING `+userColumns+`, (xmax = 0) AS inserted`,
		uuid.New(), email, name, avatarURL, authorizedOnInsert).Scan(
		&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Role, &u.Authorized,
		&u.CreatedAt, &u.NameOverridden, &u.AvatarOverridden, &inserted)
	return &u, inserted, err
}

// EnsureUserByOAuth resolves a (provider, providerUserID) pair to a local
// user, creating both the user row and the oauth_accounts binding on first
// sign-in. Unlike UpsertUser this path NEVER writes to users.email -- it
// inserts NULL, so the UNIQUE constraint on email is preserved without
// collapsing multiple email-less rows onto a single empty-string key. Used
// by social providers (Discord / Apple / Facebook / Twitter) whose IdP may
// not surface an email at all.
//
// On subsequent sign-ins the same tuple returns the existing user and
// refreshes name/avatar (honoring the _overridden flags just like UpsertUser).
// The second return value reports whether the user row was newly created.
//
// NOTE: identity-only -- writes users but never platform_members, matching
// the contract established by auth.EnsureIdentity for downstream auth.
func (s *Store) EnsureUserByOAuth(ctx context.Context, provider, providerUserID, name, avatarURL string) (*User, bool, error) {
	if provider == "" || providerUserID == "" {
		return nil, false, fmt.Errorf("provider and providerUserID required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	var userID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT user_id FROM oauth_accounts WHERE provider = $1 AND provider_user_id = $2`,
		provider, providerUserID).Scan(&userID)
	if err == pgx.ErrNoRows {
		userID = uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, name, avatar_url, role, authorized, created_at)
			VALUES ($1, NULL, $2, $3, 'member', TRUE, NOW())
		`, userID, name, avatarURL); err != nil {
			return nil, false, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO oauth_accounts (provider, provider_user_id, user_id, created_at)
			VALUES ($1, $2, $3, NOW())
		`, provider, providerUserID, userID); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		u, err := s.GetUserByID(ctx, userID)
		return u, true, err
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET
			name       = CASE WHEN name_overridden   THEN name       ELSE $2 END,
			avatar_url = CASE WHEN avatar_overridden THEN avatar_url ELSE $3 END
		WHERE id = $1
	`, userID, name, avatarURL); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	u, err := s.GetUserByID(ctx, userID)
	return u, false, err
}

// GetUserByEmail returns the user with the given email, or (nil, nil) when
// no such user exists.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email), &u)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := scanUser(s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id), &u)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]*User, 0)
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u); err != nil {
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

// UpdateUserProfile updates the caller's display name and/or avatar URL. nil
// arguments leave that field unchanged. When a field is updated, the matching
// _overridden flag flips to TRUE so subsequent OAuth logins stop overwriting
// the user's customisation in UpsertUser.
func (s *Store) UpdateUserProfile(ctx context.Context, userID uuid.UUID, name *string, avatarURL *string) (*User, error) {
	if name == nil && avatarURL == nil {
		return s.GetUserByID(ctx, userID)
	}
	parts := make([]string, 0, 2)
	args := make([]any, 0, 3)
	idx := 1
	if name != nil {
		parts = append(parts, fmt.Sprintf("name = $%d, name_overridden = TRUE", idx))
		args = append(args, *name)
		idx++
	}
	if avatarURL != nil {
		parts = append(parts, fmt.Sprintf("avatar_url = $%d, avatar_overridden = TRUE", idx))
		args = append(args, *avatarURL)
		idx++
	}
	args = append(args, userID)
	q := fmt.Sprintf(`UPDATE users SET %s WHERE id = $%d RETURNING %s`,
		strings.Join(parts, ", "), idx, userColumns)
	var u User
	if err := scanUser(s.db.QueryRow(ctx, q, args...), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// ─── Platform Members ────────────────────────────────────────────────────────
//
// platform_members records a user's authorization on the muvee admin plane,
// separately from their identity in the `users` table. Subdomain-auth users
// (ensured via /api/internal/auth/identity-upsert) land in `users` only and
// never become platform members unless they cross over via project ownership
// or admin promotion.

const platformMemberColumns = `user_id, role, authorized, created_at`

func scanPlatformMember(scanner interface {
	Scan(dest ...interface{}) error
}, m *PlatformMember) error {
	return scanner.Scan(&m.UserID, &m.Role, &m.Authorized, &m.CreatedAt)
}

// GetPlatformMember returns the platform_members row for a user, or
// (nil, nil) when the user is not a platform member.
func (s *Store) GetPlatformMember(ctx context.Context, userID uuid.UUID) (*PlatformMember, error) {
	var m PlatformMember
	err := scanPlatformMember(s.db.QueryRow(ctx,
		`SELECT `+platformMemberColumns+` FROM platform_members WHERE user_id = $1`, userID), &m)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &m, err
}

// UpsertPlatformMember creates or updates the platform_members row. role is
// only applied on INSERT; existing rows keep their role (use SetPlatformMemberRole
// to change it). authorizedOnInsert mirrors the same insert-only semantics.
func (s *Store) UpsertPlatformMember(ctx context.Context, userID uuid.UUID, role UserRole, authorizedOnInsert bool) (*PlatformMember, bool, error) {
	var m PlatformMember
	var inserted bool
	err := s.db.QueryRow(ctx, `
		INSERT INTO platform_members (user_id, role, authorized, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING `+platformMemberColumns+`, (xmax = 0) AS inserted`,
		userID, role, authorizedOnInsert).Scan(
		&m.UserID, &m.Role, &m.Authorized, &m.CreatedAt, &inserted)
	return &m, inserted, err
}

func (s *Store) SetPlatformMemberRole(ctx context.Context, userID uuid.UUID, role UserRole) error {
	_, err := s.db.Exec(ctx,
		`UPDATE platform_members SET role = $1 WHERE user_id = $2`, role, userID)
	return err
}

func (s *Store) SetPlatformMemberAuthorized(ctx context.Context, userID uuid.UUID, authorized bool) error {
	_, err := s.db.Exec(ctx,
		`UPDATE platform_members SET authorized = $1 WHERE user_id = $2`, authorized, userID)
	return err
}

// IsPlatformAdmin is a hot-path helper: returns true iff the user is a
// platform_member with role='admin'.
func (s *Store) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM platform_members WHERE user_id = $1 AND role = 'admin')`,
		userID).Scan(&ok)
	return ok, err
}

// PlatformUser bundles a User with its (optional) PlatformMember row, used by
// admin/users listing. PlatformMember is nil when the user is identity-only
// (came in through subdomain auth and never crossed over to the platform).
type PlatformUser struct {
	User
	PlatformMember *PlatformMember `json:"-"`
}

// ListPlatformMemberUsers returns users joined with their platform_members
// rows. scope:
//   - "platform": only users with a platform_members row (i.e. real platform
//     members; this is what /admin/users shows by default)
//   - "all":      every user, with PlatformMember=nil for identity-only users
func (s *Store) ListPlatformMemberUsers(ctx context.Context, scope string) ([]*PlatformUser, error) {
	join := `LEFT JOIN`
	where := ``
	if scope == "platform" {
		join = `JOIN`
	}
	q := `SELECT u.id, COALESCE(u.email, '') AS email, u.name, u.avatar_url, u.role, u.authorized,
	             u.created_at, u.name_overridden, u.avatar_overridden,
	             pm.user_id, pm.role, pm.authorized, pm.created_at
	      FROM users u ` + join + ` platform_members pm ON pm.user_id = u.id ` + where + `
	      ORDER BY u.created_at`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*PlatformUser, 0)
	for rows.Next() {
		var pu PlatformUser
		var pmUserID *uuid.UUID
		var pmRole *UserRole
		var pmAuthorized *bool
		var pmCreatedAt *time.Time
		if err := rows.Scan(
			&pu.User.ID, &pu.User.Email, &pu.User.Name, &pu.User.AvatarURL,
			&pu.User.Role, &pu.User.Authorized, &pu.User.CreatedAt,
			&pu.User.NameOverridden, &pu.User.AvatarOverridden,
			&pmUserID, &pmRole, &pmAuthorized, &pmCreatedAt,
		); err != nil {
			return nil, err
		}
		if pmUserID != nil {
			pu.PlatformMember = &PlatformMember{
				UserID:     *pmUserID,
				Role:       *pmRole,
				Authorized: *pmAuthorized,
				CreatedAt:  *pmCreatedAt,
			}
		}
		out = append(out, &pu)
	}
	return out, nil
}

// ─── Projects ────────────────────────────────────────────────────────────────

// projectColumns is the full SELECT list for a Project row. git_url is COALESCEd
// so domain_only projects (which have NULL git_url) scan into an empty string.
const projectColumns = `id, name, project_type, COALESCE(git_url, '') AS git_url, git_branch, git_source, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, auth_bypass_paths, container_port, memory_limit, volume_mount_path, description, icon, tags, compose_file_path, expose_service, expose_port, pinned_node_id, image_ref, auto_deploy_enabled, last_tracked_commit_sha, last_tracked_image_digests, access_mode, fixed_host_port, fixed_node_id, created_at, updated_at, enabled_providers, branding_site_name, branding_logo_url, branding_favicon_url, branding_primary_color, branding_sidebar_bg, branding_tagline, branding_description, branding_footer_text, branding_trust_text, last_image_tag, triggers_redeploy_of, paused, sms_login_enabled`

// projectColumnsPrefixed is projectColumns with every column qualified by the
// `p.` alias. Used when the query JOINs another table (e.g. users) so bare
// column names like `id` or `name` would be ambiguous.
const projectColumnsPrefixed = `p.id, p.name, p.project_type, COALESCE(p.git_url, '') AS git_url, p.git_branch, p.git_source, p.domain_prefix, p.dockerfile_path, p.owner_id, p.auth_required, p.auth_allowed_domains, p.auth_bypass_paths, p.container_port, p.memory_limit, p.volume_mount_path, p.description, p.icon, p.tags, p.compose_file_path, p.expose_service, p.expose_port, p.pinned_node_id, p.image_ref, p.auto_deploy_enabled, p.last_tracked_commit_sha, p.last_tracked_image_digests, p.access_mode, p.fixed_host_port, p.fixed_node_id, p.created_at, p.updated_at, p.enabled_providers, p.branding_site_name, p.branding_logo_url, p.branding_favicon_url, p.branding_primary_color, p.branding_sidebar_bg, p.branding_tagline, p.branding_description, p.branding_footer_text, p.branding_trust_text, p.last_image_tag, p.triggers_redeploy_of, p.paused, p.sms_login_enabled`

// ownerJoinColumns is the tail of the SELECT list for projects queried with
// `LEFT JOIN users u ON u.id = p.owner_id`.
const ownerJoinColumns = `, COALESCE(u.name, '') AS owner_name, COALESCE(u.email, '') AS owner_email, COALESCE(u.avatar_url, '') AS owner_avatar_url`

func scanProject(scanner interface {
	Scan(dest ...interface{}) error
}, p *Project) error {
	return scanner.Scan(&p.ID, &p.Name, &p.ProjectType, &p.GitURL, &p.GitBranch, &p.GitSource, &p.DomainPrefix, &p.DockerfilePath, &p.OwnerID, &p.AuthRequired, &p.AuthAllowedDomains, &p.AuthBypassPaths, &p.ContainerPort, &p.MemoryLimit, &p.VolumeMountPath, &p.Description, &p.Icon, &p.Tags, &p.ComposeFilePath, &p.ExposeService, &p.ExposePort, &p.PinnedNodeID, &p.ImageRef, &p.AutoDeployEnabled, &p.LastTrackedCommitSHA, &p.LastTrackedImageDigests, &p.AccessMode, &p.FixedHostPort, &p.FixedNodeID, &p.CreatedAt, &p.UpdatedAt, &p.EnabledProviders, &p.BrandingSiteName, &p.BrandingLogoURL, &p.BrandingFaviconURL, &p.BrandingPrimaryColor, &p.BrandingSidebarBg, &p.BrandingTagline, &p.BrandingDescription, &p.BrandingFooterText, &p.BrandingTrustText, &p.LastImageTag, &p.TriggersRedeployOf, &p.Paused, &p.SMSLoginEnabled)
}

func scanProjectWithOwner(scanner interface {
	Scan(dest ...interface{}) error
}, p *Project) error {
	return scanner.Scan(&p.ID, &p.Name, &p.ProjectType, &p.GitURL, &p.GitBranch, &p.GitSource, &p.DomainPrefix, &p.DockerfilePath, &p.OwnerID, &p.AuthRequired, &p.AuthAllowedDomains, &p.AuthBypassPaths, &p.ContainerPort, &p.MemoryLimit, &p.VolumeMountPath, &p.Description, &p.Icon, &p.Tags, &p.ComposeFilePath, &p.ExposeService, &p.ExposePort, &p.PinnedNodeID, &p.ImageRef, &p.AutoDeployEnabled, &p.LastTrackedCommitSHA, &p.LastTrackedImageDigests, &p.AccessMode, &p.FixedHostPort, &p.FixedNodeID, &p.CreatedAt, &p.UpdatedAt, &p.EnabledProviders, &p.BrandingSiteName, &p.BrandingLogoURL, &p.BrandingFaviconURL, &p.BrandingPrimaryColor, &p.BrandingSidebarBg, &p.BrandingTagline, &p.BrandingDescription, &p.BrandingFooterText, &p.BrandingTrustText, &p.LastImageTag, &p.TriggersRedeployOf, &p.Paused, &p.SMSLoginEnabled, &p.OwnerName, &p.OwnerEmail, &p.OwnerAvatarURL)
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
	if p.ProjectType == ProjectTypeImage {
		if p.ContainerPort == 0 {
			p.ContainerPort = 8080
		}
		if p.MemoryLimit == "" {
			p.MemoryLimit = "4g"
		}
	}
	// git_url is stored as NULL when empty so the NOT NULL-dropped column
	// stays consistent semantically for domain_only rows.
	var gitURL interface{}
	if p.GitURL != "" {
		gitURL = p.GitURL
	}
	if p.LastTrackedImageDigests == "" {
		p.LastTrackedImageDigests = "{}"
	}
	if p.AccessMode == "" {
		p.AccessMode = ProjectAccessModePublic
	}
	if p.TriggersRedeployOf == "" {
		p.TriggersRedeployOf = "[]"
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO projects (id, name, project_type, git_url, git_branch, git_source, domain_prefix, dockerfile_path, owner_id, auth_required, auth_allowed_domains, auth_bypass_paths, container_port, memory_limit, volume_mount_path, description, icon, tags, compose_file_path, expose_service, expose_port, pinned_node_id, image_ref, auto_deploy_enabled, last_tracked_commit_sha, last_tracked_image_digests, access_mode, fixed_host_port, fixed_node_id, created_at, updated_at, enabled_providers, branding_site_name, branding_logo_url, branding_favicon_url, branding_primary_color, branding_sidebar_bg, branding_tagline, branding_description, branding_footer_text, branding_trust_text, last_image_tag, triggers_redeploy_of)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43)
	`, p.ID, p.Name, p.ProjectType, gitURL, p.GitBranch, p.GitSource, p.DomainPrefix, p.DockerfilePath, p.OwnerID, p.AuthRequired, p.AuthAllowedDomains, p.AuthBypassPaths, p.ContainerPort, p.MemoryLimit, p.VolumeMountPath, p.Description, p.Icon, p.Tags, p.ComposeFilePath, p.ExposeService, p.ExposePort, p.PinnedNodeID, p.ImageRef, p.AutoDeployEnabled, p.LastTrackedCommitSHA, p.LastTrackedImageDigests, p.AccessMode, p.FixedHostPort, p.FixedNodeID, p.CreatedAt, p.UpdatedAt, p.EnabledProviders, p.BrandingSiteName, p.BrandingLogoURL, p.BrandingFaviconURL, p.BrandingPrimaryColor, p.BrandingSidebarBg, p.BrandingTagline, p.BrandingDescription, p.BrandingFooterText, p.BrandingTrustText, p.LastImageTag, p.TriggersRedeployOf)
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO project_members (project_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, p.ID, p.OwnerID)
	return p, err
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (*Project, error) {
	var p Project
	err := scanProjectWithOwner(s.db.QueryRow(ctx,
		`SELECT `+projectColumnsPrefixed+ownerJoinColumns+
			` FROM projects p LEFT JOIN users u ON u.id = p.owner_id WHERE p.id = $1`, id), &p)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

func (s *Store) ListProjectsForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]*Project, error) {
	var query string
	var args []interface{}
	if isAdmin {
		query = `SELECT ` + projectColumnsPrefixed + ownerJoinColumns +
			` FROM projects p LEFT JOIN users u ON u.id = p.owner_id ORDER BY p.created_at DESC`
	} else {
		query = `SELECT ` + projectColumnsPrefixed + ownerJoinColumns + `
			FROM projects p
			JOIN project_members pm ON p.id = pm.project_id
			LEFT JOIN users u ON u.id = p.owner_id
			WHERE pm.user_id = $1
			ORDER BY p.created_at DESC`
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
		if err := scanProjectWithOwner(rows, &p); err != nil {
			return nil, err
		}
		projects = append(projects, &p)
	}
	return projects, nil
}

// UpdateProjectOwner reassigns a project to a new owner. The new owner is also
// granted access via project_members (existing members, including the old
// owner, are left untouched so their access is preserved unless explicitly
// revoked elsewhere).
func (s *Store) UpdateProjectOwner(ctx context.Context, projectID, newOwnerID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET owner_id = $1, updated_at = $2 WHERE id = $3`,
		newOwnerID, time.Now(), projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		projectID, newOwnerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	if p.AccessMode == "" {
		p.AccessMode = ProjectAccessModePublic
	}
	if p.TriggersRedeployOf == "" {
		p.TriggersRedeployOf = "[]"
	}
	_, err := s.db.Exec(ctx, `
		UPDATE projects SET name=$1, git_url=$2, git_branch=$3, git_source=$4, domain_prefix=$5, dockerfile_path=$6, auth_required=$7, auth_allowed_domains=$8, auth_bypass_paths=$9, container_port=$10, memory_limit=$11, volume_mount_path=$12, description=$13, icon=$14, tags=$15, compose_file_path=$16, expose_service=$17, expose_port=$18, image_ref=$19, auto_deploy_enabled=$20, access_mode=$21, fixed_host_port=$22, fixed_node_id=$23, updated_at=$24, enabled_providers=$25, branding_site_name=$26, branding_logo_url=$27, branding_favicon_url=$28, branding_primary_color=$29, branding_sidebar_bg=$30, branding_tagline=$31, branding_description=$32, branding_footer_text=$33, branding_trust_text=$34, triggers_redeploy_of=$35, sms_login_enabled=$36 WHERE id=$37
	`, p.Name, gitURL, p.GitBranch, p.GitSource, p.DomainPrefix, p.DockerfilePath, p.AuthRequired, p.AuthAllowedDomains, p.AuthBypassPaths, p.ContainerPort, p.MemoryLimit, p.VolumeMountPath, p.Description, p.Icon, p.Tags, p.ComposeFilePath, p.ExposeService, p.ExposePort, p.ImageRef, p.AutoDeployEnabled, p.AccessMode, p.FixedHostPort, p.FixedNodeID, p.UpdatedAt, p.EnabledProviders, p.BrandingSiteName, p.BrandingLogoURL, p.BrandingFaviconURL, p.BrandingPrimaryColor, p.BrandingSidebarBg, p.BrandingTagline, p.BrandingDescription, p.BrandingFooterText, p.BrandingTrustText, p.TriggersRedeployOf, p.SMSLoginEnabled, p.ID)
	return err
}

// SetProjectLastImageTag records the most recent image tag pushed by a
// ProjectTypeBuild project's builder run. Updated immediately after the
// `docker buildx build --push` step succeeds (before the deployment row is
// closed) so downstream compose / image projects can resolve the new tag
// without waiting on the build deployment status.
func (s *Store) SetProjectLastImageTag(ctx context.Context, projectID uuid.UUID, tag string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE projects SET last_image_tag=$1, updated_at=$2 WHERE id=$3`,
		tag, time.Now(), projectID)
	return err
}

// SetProjectLastTrackedCommitSHA records the commit SHA the auto-deploy
// watcher last triggered a deployment for. Updated on the project row
// regardless of whether the build/deploy ultimately succeeds — failures will
// still consume the event so we don't loop indefinitely on a broken commit.
func (s *Store) SetProjectLastTrackedCommitSHA(ctx context.Context, projectID uuid.UUID, sha string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE projects SET last_tracked_commit_sha=$1, updated_at=$2 WHERE id=$3`,
		sha, time.Now(), projectID)
	return err
}

// SetProjectLastTrackedImageDigests stores the JSON-encoded map of
// image-string -> digest the image watcher last observed. Pass "{}" to
// clear (e.g. when the compose file changes and the previous map is no
// longer comparable).
func (s *Store) SetProjectLastTrackedImageDigests(ctx context.Context, projectID uuid.UUID, digestsJSON string) error {
	if digestsJSON == "" {
		digestsJSON = "{}"
	}
	_, err := s.db.Exec(ctx,
		`UPDATE projects SET last_tracked_image_digests=$1, updated_at=$2 WHERE id=$3`,
		digestsJSON, time.Now(), projectID)
	return err
}

// SetProjectPaused toggles the project's soft-pause flag. While true, every
// deploy path is gated in scheduler.TriggerDeployment; the container(s) are
// stopped/started out-of-band by the pause/unpause agent tasks.
func (s *Store) SetProjectPaused(ctx context.Context, projectID uuid.UUID, paused bool) error {
	_, err := s.db.Exec(ctx,
		`UPDATE projects SET paused=$1, updated_at=$2 WHERE id=$3`,
		paused, time.Now(), projectID)
	return err
}

// ListAutoDeployProjects returns every project that has auto_deploy_enabled
// set, optionally filtered by git_source. Pass an empty source to get both.
// Used by the control-plane poller. Paused projects are excluded — a paused
// project must not be auto-redeployed out from under the operator.
func (s *Store) ListAutoDeployProjects(ctx context.Context, gitSource string) ([]*Project, error) {
	query := `SELECT ` + projectColumns + ` FROM projects WHERE auto_deploy_enabled = TRUE AND paused = FALSE AND project_type IN ('deployment', 'compose', 'image', 'build')`
	var args []interface{}
	if gitSource != "" {
		query += ` AND git_source = $1`
		args = append(args, gitSource)
	}
	rows, err := s.db.Query(ctx, query, args...)
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

// SetProjectPinnedNode persists the deploy node a compose project is pinned to.
// The node is chosen on first deploy and reused thereafter so docker named
// volumes survive across redeploys.
func (s *Store) SetProjectPinnedNode(ctx context.Context, projectID, nodeID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE projects SET pinned_node_id = $1, updated_at = $2 WHERE id = $3`,
		nodeID, time.Now(), projectID)
	return err
}

// IsFixedPortInUse reports whether another project has already claimed the
// (nodeID, port) pair as its fixed-port binding. Used by the API layer to give
// admins instant feedback when configuring fixed_host_port. Pass excludeID
// equal to the project being edited so its own current binding doesn't count
// as a conflict (zero UUID disables the exclusion).
func (s *Store) IsFixedPortInUse(ctx context.Context, nodeID uuid.UUID, port int, excludeID uuid.UUID) (bool, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM projects
		WHERE fixed_node_id = $1 AND fixed_host_port = $2 AND id <> $3
	`, nodeID, port, excludeID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
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
		       p.auth_required, p.access_mode, p.updated_at,
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
			&info.AuthRequired, &info.AccessMode, &info.UpdatedAt,
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
		       p.auth_required, p.access_mode, p.updated_at,
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
			&info.AuthRequired, &info.AccessMode, &info.UpdatedAt,
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

// ─── Project Access (downstream service ACL) ─────────────────────────────────

// ListProjectAccessUsers returns the explicit per-project allow-list, joined
// with users for display. Project owners and system admins are NOT in this
// list — they are implicitly allowed and tracked elsewhere.
func (s *Store) ListProjectAccessUsers(ctx context.Context, projectID uuid.UUID) ([]*ProjectAccessUser, error) {
	rows, err := s.db.Query(ctx, `
		SELECT pau.project_id, pau.user_id, pau.added_by, pau.added_at,
		       COALESCE(u.email, '') AS email, u.name, u.avatar_url
		FROM project_access_users pau
		JOIN users u ON u.id = pau.user_id
		WHERE pau.project_id = $1
		ORDER BY pau.added_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ProjectAccessUser, 0)
	for rows.Next() {
		var pau ProjectAccessUser
		if err := rows.Scan(&pau.ProjectID, &pau.UserID, &pau.AddedBy, &pau.AddedAt, &pau.Email, &pau.Name, &pau.AvatarURL); err != nil {
			return nil, err
		}
		out = append(out, &pau)
	}
	return out, nil
}

func (s *Store) AddProjectAccessUser(ctx context.Context, projectID, userID uuid.UUID, addedBy *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO project_access_users (project_id, user_id, added_by, added_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (project_id, user_id) DO NOTHING
	`, projectID, userID, addedBy)
	return err
}

func (s *Store) RemoveProjectAccessUser(ctx context.Context, projectID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM project_access_users WHERE project_id=$1 AND user_id=$2`, projectID, userID)
	return err
}

// AccessCheckResult is the full outcome of IsProjectAccessAllowedByEmail.
// Mode is the project's access_mode (or "" if the user / project lookup
// failed before reaching the project row). UserID is the looked-up user's
// id when the lookup succeeded — even when Allowed is false, callers can
// use it to record visit attempts or correlate logs. IsAdmin is true when
// the user holds the platform admin role.
type AccessCheckResult struct {
	Allowed bool
	Mode    string
	UserID  uuid.UUID
	IsAdmin bool
}

// IsProjectAccessAllowedByEmail decides whether a user (looked up by email)
// is permitted to reach the project's downstream service via Traefik
// ForwardAuth. Allow rules: admin users always pass; public projects pass any
// registered user; private projects only pass the owner or users explicitly
// listed in project_access_users.
func (s *Store) IsProjectAccessAllowedByEmail(ctx context.Context, email string, projectID uuid.UUID) (AccessCheckResult, error) {
	var res AccessCheckResult
	var role UserRole
	err := s.db.QueryRow(ctx, `SELECT id, role FROM users WHERE email = $1`, email).Scan(&res.UserID, &role)
	if err == pgx.ErrNoRows {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	res.IsAdmin = role == UserRoleAdmin
	if res.IsAdmin {
		res.Allowed = true
		return res, nil
	}
	var ownerID uuid.UUID
	err = s.db.QueryRow(ctx, `SELECT access_mode, owner_id FROM projects WHERE id = $1`, projectID).Scan(&res.Mode, &ownerID)
	if err == pgx.ErrNoRows {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if res.Mode == ProjectAccessModePublic {
		res.Allowed = true
		return res, nil
	}
	if ownerID == res.UserID {
		res.Allowed = true
		return res, nil
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM project_access_users WHERE project_id=$1 AND user_id=$2`, projectID, res.UserID).Scan(&n); err != nil {
		return res, err
	}
	res.Allowed = n > 0
	return res, nil
}

// ─── Project Visits (downstream service hit counter) ─────────────────────────

// RecordProjectVisitsBatch UPSERTs a batch of (project, user, seen_at) tuples
// into project_visits in a single round-trip. visit_count is incremented and
// last_seen_at is bumped to the maximum of the existing value and the new
// sample (so out-of-order arrivals from a buffered channel can't move it
// backwards). first_seen_at is set on insert and never updated.
//
// items[i].VisitCount is interpreted as the number of hits to add (typically
// 1, but a debounced channel may aggregate). Other display fields on
// ProjectVisit (Email/Name/etc) are ignored — this method writes raw counters
// only.
func (s *Store) RecordProjectVisitsBatch(ctx context.Context, items []ProjectVisit) error {
	if len(items) == 0 {
		return nil
	}
	const cols = 4
	args := make([]interface{}, 0, len(items)*cols)
	values := make([]string, 0, len(items))
	for i, v := range items {
		base := i*cols + 1
		values = append(values, fmt.Sprintf("($%d, $%d, $%d, $%d)", base, base+1, base+2, base+3))
		incr := v.VisitCount
		if incr <= 0 {
			incr = 1
		}
		args = append(args, v.ProjectID, v.UserID, v.LastSeenAt, incr)
	}
	query := `
		INSERT INTO project_visits (project_id, user_id, last_seen_at, visit_count)
		VALUES ` + strings.Join(values, ", ") + `
		ON CONFLICT (project_id, user_id) DO UPDATE
		SET last_seen_at = GREATEST(project_visits.last_seen_at, EXCLUDED.last_seen_at),
		    visit_count  = project_visits.visit_count + EXCLUDED.visit_count`
	_, err := s.db.Exec(ctx, query, args...)
	return err
}

// ListProjectVisits returns recent unique visitors of a project, joined with
// users for display and LEFT-joined with project_access_users to mark whether
// each visitor is in the allow-list (so the UI can hide / show the
// "add to allow-list" button per row).
func (s *Store) ListProjectVisits(ctx context.Context, projectID uuid.UUID, limit int) ([]*ProjectVisit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT pv.project_id, pv.user_id, pv.first_seen_at, pv.last_seen_at, pv.visit_count,
		       COALESCE(u.email, '') AS email, u.name, u.avatar_url,
		       (pau.user_id IS NOT NULL) AS in_allow_list
		FROM project_visits pv
		JOIN users u ON u.id = pv.user_id
		LEFT JOIN project_access_users pau
		  ON pau.project_id = pv.project_id AND pau.user_id = pv.user_id
		WHERE pv.project_id = $1
		ORDER BY pv.last_seen_at DESC
		LIMIT $2
	`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ProjectVisit, 0)
	for rows.Next() {
		var v ProjectVisit
		if err := rows.Scan(&v.ProjectID, &v.UserID, &v.FirstSeenAt, &v.LastSeenAt, &v.VisitCount,
			&v.Email, &v.Name, &v.AvatarURL, &v.InAllowList); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, nil
}

// ─── Project Access Requests (private-project request inbox) ─────────────────

// ErrAccessRequestAlreadyApproved is returned by CreateAccessRequest when the
// user is already in project_access_users for the project (so a request would
// be redundant). Callers translate this to a 400 with a friendly message.
var ErrAccessRequestAlreadyApproved = errors.New("user already has access to this project")

// CreateAccessRequest inserts a pending request, or returns the existing
// pending row if one already exists for the same (project, user). If the
// user is already in project_access_users, returns ErrAccessRequestAlreadyApproved
// without inserting (the partial unique index would have allowed the insert,
// but it's a useless row).
func (s *Store) CreateAccessRequest(ctx context.Context, projectID, userID uuid.UUID, reason string) (*ProjectAccessRequest, error) {
	var alreadyAllowed bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM project_access_users WHERE project_id=$1 AND user_id=$2)`,
		projectID, userID).Scan(&alreadyAllowed); err != nil {
		return nil, err
	}
	if alreadyAllowed {
		return nil, ErrAccessRequestAlreadyApproved
	}
	var ownerID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT owner_id FROM projects WHERE id=$1`, projectID).Scan(&ownerID); err != nil {
		return nil, err
	}
	if ownerID == userID {
		return nil, ErrAccessRequestAlreadyApproved
	}
	var existingID uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM project_access_requests WHERE project_id=$1 AND user_id=$2 AND status='pending'`,
		projectID, userID).Scan(&existingID)
	if err == nil {
		// Reuse: bump reason if the new submission has a non-empty reason.
		if strings.TrimSpace(reason) != "" {
			if _, err := s.db.Exec(ctx,
				`UPDATE project_access_requests SET reason=$1, requested_at=NOW() WHERE id=$2`,
				reason, existingID); err != nil {
				return nil, err
			}
		}
		return s.GetAccessRequest(ctx, existingID)
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}
	var req ProjectAccessRequest
	err = s.db.QueryRow(ctx, `
		INSERT INTO project_access_requests (project_id, user_id, reason, status, requested_at)
		VALUES ($1, $2, $3, 'pending', NOW())
		RETURNING id, project_id, user_id, reason, status, requested_at, decided_at, decided_by
	`, projectID, userID, reason).Scan(&req.ID, &req.ProjectID, &req.UserID, &req.Reason, &req.Status,
		&req.RequestedAt, &req.DecidedAt, &req.DecidedBy)
	if err != nil {
		return nil, err
	}
	return &req, nil
}

// GetAccessRequest fetches a single request by id, joined with the requesting
// user for display.
func (s *Store) GetAccessRequest(ctx context.Context, id uuid.UUID) (*ProjectAccessRequest, error) {
	var req ProjectAccessRequest
	err := s.db.QueryRow(ctx, `
		SELECT r.id, r.project_id, r.user_id, r.reason, r.status, r.requested_at, r.decided_at, r.decided_by,
		       COALESCE(u.email, '') AS email, u.name, u.avatar_url
		FROM project_access_requests r
		JOIN users u ON u.id = r.user_id
		WHERE r.id = $1
	`, id).Scan(&req.ID, &req.ProjectID, &req.UserID, &req.Reason, &req.Status,
		&req.RequestedAt, &req.DecidedAt, &req.DecidedBy,
		&req.UserEmail, &req.UserName, &req.UserAvatarURL)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &req, nil
}

// ListAccessRequests returns requests for a project. statusFilter == "" means
// all statuses; otherwise pass one of pending/approved/denied to filter.
func (s *Store) ListAccessRequests(ctx context.Context, projectID uuid.UUID, statusFilter ProjectAccessRequestStatus) ([]*ProjectAccessRequest, error) {
	query := `
		SELECT r.id, r.project_id, r.user_id, r.reason, r.status, r.requested_at, r.decided_at, r.decided_by,
		       COALESCE(u.email, '') AS email, u.name, u.avatar_url
		FROM project_access_requests r
		JOIN users u ON u.id = r.user_id
		WHERE r.project_id = $1`
	args := []interface{}{projectID}
	if statusFilter != "" {
		query += ` AND r.status = $2`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY r.requested_at DESC`
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ProjectAccessRequest, 0)
	for rows.Next() {
		var r ProjectAccessRequest
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.UserID, &r.Reason, &r.Status,
			&r.RequestedAt, &r.DecidedAt, &r.DecidedBy,
			&r.UserEmail, &r.UserName, &r.UserAvatarURL); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, nil
}

// ListPendingRequestsForOwner returns pending requests across all projects
// owned by the given user, joined with project display fields. Used by the
// owner-dashboard / topbar badge.
func (s *Store) ListPendingRequestsForOwner(ctx context.Context, ownerID uuid.UUID) ([]*ProjectAccessRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.project_id, r.user_id, r.reason, r.status, r.requested_at, r.decided_at, r.decided_by,
		       COALESCE(u.email, '') AS email, u.name, u.avatar_url,
		       p.name, p.domain_prefix
		FROM project_access_requests r
		JOIN projects p ON p.id = r.project_id
		JOIN users    u ON u.id = r.user_id
		WHERE p.owner_id = $1 AND r.status = 'pending'
		ORDER BY r.requested_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ProjectAccessRequest, 0)
	for rows.Next() {
		var r ProjectAccessRequest
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.UserID, &r.Reason, &r.Status,
			&r.RequestedAt, &r.DecidedAt, &r.DecidedBy,
			&r.UserEmail, &r.UserName, &r.UserAvatarURL,
			&r.ProjectName, &r.ProjectDomainPrefix); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, nil
}

// DecideAccessRequest moves a pending request to approved or denied. When
// approving, the user is INSERTed into project_access_users in the same
// transaction so the grant goes live atomically. Returns the updated request.
// Returns nil, nil if the request id does not exist or has already been decided.
func (s *Store) DecideAccessRequest(ctx context.Context, requestID uuid.UUID, status ProjectAccessRequestStatus, decidedBy uuid.UUID) (*ProjectAccessRequest, error) {
	if status != ProjectAccessRequestApproved && status != ProjectAccessRequestDenied {
		return nil, fmt.Errorf("invalid decision status: %q", status)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var projectID, userID uuid.UUID
	var curStatus ProjectAccessRequestStatus
	err = tx.QueryRow(ctx,
		`SELECT project_id, user_id, status FROM project_access_requests WHERE id=$1 FOR UPDATE`,
		requestID).Scan(&projectID, &userID, &curStatus)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if curStatus != ProjectAccessRequestPending {
		// Already decided — refuse to change. Return the current row so the
		// caller can surface a clear "already decided" message.
		return s.GetAccessRequest(ctx, requestID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE project_access_requests
		SET status=$1, decided_at=NOW(), decided_by=$2
		WHERE id=$3
	`, status, decidedBy, requestID); err != nil {
		return nil, err
	}
	if status == ProjectAccessRequestApproved {
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_access_users (project_id, user_id, added_by, added_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (project_id, user_id) DO NOTHING
		`, projectID, userID, decidedBy); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetAccessRequest(ctx, requestID)
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

// UpdateDeploymentHostPortByDomainPrefix overwrites the host_port column on the
// currently running deployment of the project identified by domain_prefix when
// the reported port differs from the persisted value. Used by the agent's
// periodic container-status heartbeat to recover from random host port changes
// after a docker restart.
func (s *Store) UpdateDeploymentHostPortByDomainPrefix(ctx context.Context, domainPrefix string, hostPort int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE deployments d
		SET host_port = $1,
		    updated_at = NOW()
		FROM projects p
		WHERE d.project_id = p.id
		  AND p.domain_prefix = $2
		  AND d.status = 'running'
		  AND d.host_port IS DISTINCT FROM $1
	`, hostPort, domainPrefix)
	return err
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
		SELECT d.id, d.project_id, p.domain_prefix, p.auth_required, p.auth_allowed_domains, p.auth_bypass_paths, p.access_mode, n.host_ip, d.host_port
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
		if err := rows.Scan(&r.DeploymentID, &r.ProjectID, &r.DomainPrefix, &r.AuthRequired, &r.AuthAllowedDomains, &r.AuthBypassPaths, &r.AccessMode, &r.HostIP, &r.HostPort); err != nil {
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
	var nodeID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT d.id, d.project_id, p.domain_prefix, p.auth_required, p.auth_allowed_domains, p.auth_bypass_paths, p.access_mode, n.host_ip, d.host_port, d.node_id
		FROM deployments d
		JOIN projects p ON d.project_id = p.id
		JOIN nodes n ON d.node_id = n.id
		WHERE d.project_id = $1 AND d.status = 'running' AND d.host_port > 0 AND n.host_ip != ''
		ORDER BY d.created_at DESC
		LIMIT 1
	`, projectID).Scan(&r.DeploymentID, &r.ProjectID, &r.DomainPrefix, &r.AuthRequired, &r.AuthAllowedDomains, &r.AuthBypassPaths, &r.AccessMode, &r.HostIP, &r.HostPort, &nodeID)
	if err == nil {
		r.NodeID = &nodeID
	}
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

func (s *Store) GetNode(ctx context.Context, id uuid.UUID) (*Node, error) {
	var n Node
	err := s.db.QueryRow(ctx, `SELECT id, hostname, role, host_ip, max_storage_bytes, used_storage_bytes, last_seen_at, created_at, health_report FROM nodes WHERE id = $1`, id).
		Scan(&n.ID, &n.Hostname, &n.Role, &n.HostIP, &n.MaxStorageBytes, &n.UsedStorageBytes, &n.LastSeenAt, &n.CreatedAt, &n.HealthReport)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &n, err
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

// ─── Project Password Accounts ────────────────────────────────────────────────

const passwordAccountColumns = `id, project_id, username, email, password_hash, display_name, disabled, created_at`

func scanPasswordAccount(row pgx.Row, a *ProjectPasswordAccount) error {
	return row.Scan(&a.ID, &a.ProjectID, &a.Username, &a.Email, &a.PasswordHash,
		&a.DisplayName, &a.Disabled, &a.CreatedAt)
}

// CreateProjectPasswordAccount inserts a new demo account. The caller is
// responsible for hashing the password (bcrypt) before calling.
func (s *Store) CreateProjectPasswordAccount(ctx context.Context, projectID uuid.UUID, username, email, passwordHash, displayName string) (*ProjectPasswordAccount, error) {
	var a ProjectPasswordAccount
	err := scanPasswordAccount(s.db.QueryRow(ctx, `
		INSERT INTO project_password_accounts (id, project_id, username, email, password_hash, display_name, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		RETURNING `+passwordAccountColumns,
		uuid.New(), projectID, username, email, passwordHash, displayName), &a)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListProjectPasswordAccounts returns all demo accounts of a project,
// including disabled ones (the management UI shows both).
func (s *Store) ListProjectPasswordAccounts(ctx context.Context, projectID uuid.UUID) ([]*ProjectPasswordAccount, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+passwordAccountColumns+` FROM project_password_accounts
		WHERE project_id = $1 ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*ProjectPasswordAccount, 0)
	for rows.Next() {
		var a ProjectPasswordAccount
		if err := scanPasswordAccount(rows, &a); err != nil {
			return nil, err
		}
		items = append(items, &a)
	}
	return items, nil
}

// CountEnabledProjectPasswordAccounts reports how many non-disabled demo
// accounts a project has. The downstream login page shows the password form
// only when this is > 0.
func (s *Store) CountEnabledProjectPasswordAccounts(ctx context.Context, projectID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM project_password_accounts
		WHERE project_id = $1 AND NOT disabled`, projectID).Scan(&n)
	return n, err
}

// GetProjectPasswordAccountByUsername resolves a (project, username) pair for
// login verification. Returns (nil, nil) when no such account exists.
func (s *Store) GetProjectPasswordAccountByUsername(ctx context.Context, projectID uuid.UUID, username string) (*ProjectPasswordAccount, error) {
	var a ProjectPasswordAccount
	err := scanPasswordAccount(s.db.QueryRow(ctx, `
		SELECT `+passwordAccountColumns+` FROM project_password_accounts
		WHERE project_id = $1 AND username = $2`, projectID, username), &a)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateProjectPasswordAccount updates the mutable fields of a demo account.
// Nil pointers keep the current value. The projectID is part of the WHERE
// clause so a caller can never mutate another project's account by id;
// (nil, nil) is returned when the (project, id) pair does not match.
func (s *Store) UpdateProjectPasswordAccount(ctx context.Context, projectID, id uuid.UUID, email *string, passwordHash *string, displayName *string, disabled *bool) (*ProjectPasswordAccount, error) {
	var a ProjectPasswordAccount
	err := scanPasswordAccount(s.db.QueryRow(ctx, `
		UPDATE project_password_accounts SET
			email         = COALESCE($3, email),
			password_hash = COALESCE($4, password_hash),
			display_name  = COALESCE($5, display_name),
			disabled      = COALESCE($6, disabled)
		WHERE id = $1 AND project_id = $2
		RETURNING `+passwordAccountColumns, id, projectID, email, passwordHash, displayName, disabled), &a)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// DeleteProjectPasswordAccount removes a demo account. The oauth_accounts
// binding (provider='password') and its users row are left in place so any
// sessions/audit trails keep resolving; the account simply can no longer
// sign in.
func (s *Store) DeleteProjectPasswordAccount(ctx context.Context, projectID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM project_password_accounts WHERE id = $1 AND project_id = $2`, id, projectID)
	return err
}

// ─── SMS Verification Codes ───────────────────────────────────────────────────

const smsCodeColumns = `id, phone, project_id, code_hash, expires_at, attempts, consumed_at, created_at`

func scanSMSCode(row pgx.Row, c *SMSVerificationCode) error {
	return row.Scan(&c.ID, &c.Phone, &c.ProjectID, &c.CodeHash, &c.ExpiresAt,
		&c.Attempts, &c.ConsumedAt, &c.CreatedAt)
}

// CreateSMSCode inserts a new verification code. The caller hashes the code
// (sha256) and computes expiresAt before calling; the plaintext never reaches
// the store.
func (s *Store) CreateSMSCode(ctx context.Context, projectID *uuid.UUID, phone, codeHash string, expiresAt time.Time) (*SMSVerificationCode, error) {
	var c SMSVerificationCode
	err := scanSMSCode(s.db.QueryRow(ctx, `
		INSERT INTO sms_verification_codes (id, phone, project_id, code_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING `+smsCodeColumns,
		uuid.New(), phone, projectID, codeHash, expiresAt), &c)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// LatestUnconsumedSMSCode returns the most recent not-yet-consumed code for a
// (project, phone) pair, or (nil, nil) when there is none. A nil projectID
// matches platform login codes (project_id IS NULL). Expiry and the attempt
// cap are enforced by the caller so it can distinguish the failure modes
// (expired vs too-many-attempts vs wrong code).
func (s *Store) LatestUnconsumedSMSCode(ctx context.Context, projectID *uuid.UUID, phone string) (*SMSVerificationCode, error) {
	var c SMSVerificationCode
	// IS NOT DISTINCT FROM matches NULL=NULL (platform) and id=id (downstream)
	// uniformly, so one query serves both scopes.
	err := scanSMSCode(s.db.QueryRow(ctx, `
		SELECT `+smsCodeColumns+` FROM sms_verification_codes
		WHERE project_id IS NOT DISTINCT FROM $1 AND phone = $2 AND consumed_at IS NULL
		ORDER BY created_at DESC LIMIT 1`, projectID, phone), &c)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// IncrementSMSCodeAttempts bumps the failed-attempt counter for a code.
func (s *Store) IncrementSMSCodeAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE sms_verification_codes SET attempts = attempts + 1 WHERE id = $1`, id)
	return err
}

// ConsumeSMSCode marks a code as used so it can never be replayed.
func (s *Store) ConsumeSMSCode(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE sms_verification_codes SET consumed_at = NOW() WHERE id = $1`, id)
	return err
}

// RecordSMSSend appends a send-ledger row for rate limiting (one per code
// sent). projectID is nil for platform codes, set for downstream project
// codes. The code itself is not stored — Aliyun PNVS owns it.
func (s *Store) RecordSMSSend(ctx context.Context, projectID *uuid.UUID, phone string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO sms_verification_codes (id, phone, project_id, created_at)
		VALUES ($1, $2, $3, NOW())`, uuid.New(), phone, projectID)
	return err
}

// CountSMSCodesSince counts how many codes were issued to a phone number
// (across all projects) since a cutoff. Used for resend throttling and the
// daily send cap so one number cannot be used to burn SMS quota.
func (s *Store) CountSMSCodesSince(ctx context.Context, phone string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM sms_verification_codes
		WHERE phone = $1 AND created_at >= $2`, phone, since).Scan(&n)
	return n, err
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

func (s *Store) CreateAPIToken(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, name, tokenHash string, expiresAt *time.Time) (*ApiToken, error) {
	t := &ApiToken{
		ID:        uuid.New(),
		UserID:    userID,
		ProjectID: projectID,
		Name:      name,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO api_tokens (id, user_id, project_id, name, token_hash, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, t.ID, t.UserID, t.ProjectID, t.Name, t.TokenHash, t.ExpiresAt, t.CreatedAt)
	return t, err
}

func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*ApiToken, error) {
	var t ApiToken
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, project_id, name, token_hash, last_used_at, expires_at, created_at FROM api_tokens WHERE token_hash = $1
	`, tokenHash).Scan(&t.ID, &t.UserID, &t.ProjectID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt)
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
		SELECT id, user_id, project_id, name, token_hash, last_used_at, expires_at, created_at FROM api_tokens WHERE project_id = $1 ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]*ApiToken, 0)
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, &t)
	}
	return tokens, nil
}

// ListUserAPITokens returns personal access tokens (project_id IS NULL) owned by the user.
func (s *Store) ListUserAPITokens(ctx context.Context, userID uuid.UUID) ([]*ApiToken, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, project_id, name, token_hash, last_used_at, expires_at, created_at
		FROM api_tokens
		WHERE user_id = $1 AND project_id IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]*ApiToken, 0)
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.ProjectID, &t.Name, &t.TokenHash, &t.LastUsedAt, &t.ExpiresAt, &t.CreatedAt); err != nil {
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

// DeleteUserAPIToken deletes a personal access token (project_id IS NULL) owned by the user.
// Guarded so project tokens cannot be removed via the personal-token endpoint.
func (s *Store) DeleteUserAPIToken(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2 AND project_id IS NULL`, id, userID)
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

// CreateSecret stores an encrypted secret for a user. registryAddr/registryUsername
// are only meaningful for type=registry secrets and are stored as empty for others.
func (s *Store) CreateSecret(ctx context.Context, userID uuid.UUID, name string, secretType SecretType, plaintextValue, registryAddr, registryUsername string) (*Secret, error) {
	if s.encryptionKey == nil {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY is not configured")
	}
	encrypted, err := crypto.Encrypt(s.encryptionKey, plaintextValue)
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}
	sec := &Secret{
		ID:               uuid.New(),
		UserID:           userID,
		Name:             name,
		Type:             secretType,
		EncryptedValue:   encrypted,
		ValuePreview:     computeSecretPreview(secretType, plaintextValue),
		RegistryAddr:     registryAddr,
		RegistryUsername: registryUsername,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO secrets (id, user_id, name, type, encrypted_value, value_preview, registry_addr, registry_username, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, sec.ID, sec.UserID, sec.Name, sec.Type, sec.EncryptedValue, sec.ValuePreview, sec.RegistryAddr, sec.RegistryUsername, sec.CreatedAt, sec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sec, nil
}

func (s *Store) ListSecretsForUser(ctx context.Context, userID uuid.UUID) ([]*Secret, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, type, encrypted_value, value_preview, registry_addr, registry_username, created_at, updated_at
		FROM secrets WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	secrets := make([]*Secret, 0)
	for rows.Next() {
		var sec Secret
		if err := rows.Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.EncryptedValue, &sec.ValuePreview, &sec.RegistryAddr, &sec.RegistryUsername, &sec.CreatedAt, &sec.UpdatedAt); err != nil {
			return nil, err
		}
		secrets = append(secrets, &sec)
	}
	return secrets, nil
}

func (s *Store) GetSecret(ctx context.Context, id, userID uuid.UUID) (*Secret, error) {
	var sec Secret
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, name, type, encrypted_value, value_preview, registry_addr, registry_username, created_at, updated_at
		FROM secrets WHERE id = $1 AND user_id = $2
	`, id, userID).Scan(&sec.ID, &sec.UserID, &sec.Name, &sec.Type, &sec.EncryptedValue, &sec.ValuePreview, &sec.RegistryAddr, &sec.RegistryUsername, &sec.CreatedAt, &sec.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

// RegistryAuth is a decrypted private-registry pull credential.
type RegistryAuth struct {
	Addr     string
	Username string
	Password string
}

// GetUserRegistrySecretsDecrypted returns all type=registry secrets owned by a
// user, with the token/password decrypted. Used by the scheduler to inject a
// project owner's registry credentials into compose deploys.
func (s *Store) GetUserRegistrySecretsDecrypted(ctx context.Context, userID uuid.UUID) ([]RegistryAuth, error) {
	if s.encryptionKey == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT registry_addr, registry_username, encrypted_value
		FROM secrets WHERE user_id = $1 AND type = $2
	`, userID, SecretTypeRegistry)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	auths := make([]RegistryAuth, 0)
	for rows.Next() {
		var addr, username, encVal string
		if err := rows.Scan(&addr, &username, &encVal); err != nil {
			return nil, err
		}
		password, err := crypto.Decrypt(s.encryptionKey, encVal)
		if err != nil {
			return nil, fmt.Errorf("decrypt registry secret: %w", err)
		}
		auths = append(auths, RegistryAuth{Addr: addr, Username: username, Password: password})
	}
	return auths, nil
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
		       u.name, COALESCE(u.email, '') AS email, u.avatar_url
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

// ApproveAuthorizationRequest sets the request to approved and marks the
// requesting user as an authorized platform member. Inserts a
// platform_members row when one does not already exist (the request flow
// only fires from the apex Portal, so by definition the user is asking to
// become a platform member). Also keeps the legacy users.authorized mirror
// in sync so any code path still reading the old column behaves correctly
// during the rollout window.
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

	if _, err := tx.Exec(ctx, `
		INSERT INTO platform_members (user_id, role, authorized, created_at)
		VALUES ($1, 'member', TRUE, NOW())
		ON CONFLICT (user_id) DO UPDATE SET authorized = TRUE
	`, userID); err != nil {
		return err
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
// Called when the admin switches access_mode away from "request".
func (s *Store) ClearPendingAuthorizationRequests(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM authorization_requests WHERE status = 'pending'`)
	return err
}

// ─── Invitations (email white-list) ─────────────────────────────────────────

// normalizeEmail returns the lowercased / trimmed form used as the unique key
// in the invitations table. OAuth providers can return mixed-case emails so we
// normalize on both write and lookup.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Store) CreateInvitation(ctx context.Context, email string, invitedBy uuid.UUID) (*Invitation, error) {
	email = normalizeEmail(email)
	if email == "" {
		return nil, fmt.Errorf("email required")
	}
	var inv Invitation
	err := s.db.QueryRow(ctx, `
		INSERT INTO invitations (id, email, invited_by, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (email) DO UPDATE SET invited_by = EXCLUDED.invited_by
		RETURNING id, email, invited_by, created_at
	`, uuid.New(), email, invitedBy).Scan(&inv.ID, &inv.Email, &inv.InvitedBy, &inv.CreatedAt)
	return &inv, err
}

func (s *Store) IsEmailInvited(ctx context.Context, email string) (bool, error) {
	email = normalizeEmail(email)
	if email == "" {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM invitations WHERE email = $1)`, email).Scan(&exists)
	return exists, err
}

func (s *Store) ListInvitations(ctx context.Context) ([]*Invitation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.email, i.invited_by, i.created_at,
		       COALESCE(u.name, ''), COALESCE(u.email, '')
		FROM invitations i
		LEFT JOIN users u ON u.id = i.invited_by
		ORDER BY i.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Invitation, 0)
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.InvitedBy, &inv.CreatedAt,
			&inv.InvitedByName, &inv.InvitedByEmail); err != nil {
			return nil, err
		}
		out = append(out, &inv)
	}
	return out, nil
}

func (s *Store) DeleteInvitation(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM invitations WHERE id = $1`, id)
	return err
}

// ─── Invitation Links (platform single-use + project multi-use) ────────────

// CreateInvitationLink generates a fresh PLATFORM-scoped token (project_id is
// NULL → single-use) and returns the invitation_link with the raw Token
// populated (only available here — never recoverable from the DB afterwards).
func (s *Store) CreateInvitationLink(ctx context.Context, invitedBy uuid.UUID, token, tokenHash string, expiresAt *time.Time) (*InvitationLink, error) {
	var link InvitationLink
	err := s.db.QueryRow(ctx, `
		INSERT INTO invitation_links (id, token_hash, invited_by, expires_at, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, token_hash, invited_by, project_id, max_uses, expires_at, used_at, used_by, created_at
	`, uuid.New(), tokenHash, invitedBy, expiresAt).Scan(
		&link.ID, &link.TokenHash, &link.InvitedBy, &link.ProjectID, &link.MaxUses,
		&link.ExpiresAt, &link.UsedAt, &link.UsedBy, &link.CreatedAt)
	if err != nil {
		return nil, err
	}
	link.Token = token
	return &link, nil
}

// CreateProjectInvitationLink generates a project-scoped invitation token.
// maxUses == nil means unlimited consumption until expires_at / manual revoke.
func (s *Store) CreateProjectInvitationLink(ctx context.Context, projectID, invitedBy uuid.UUID, token, tokenHash string, maxUses *int, expiresAt *time.Time) (*InvitationLink, error) {
	var link InvitationLink
	err := s.db.QueryRow(ctx, `
		INSERT INTO invitation_links (id, token_hash, invited_by, project_id, max_uses, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		RETURNING id, token_hash, invited_by, project_id, max_uses, expires_at, used_at, used_by, created_at
	`, uuid.New(), tokenHash, invitedBy, projectID, maxUses, expiresAt).Scan(
		&link.ID, &link.TokenHash, &link.InvitedBy, &link.ProjectID, &link.MaxUses,
		&link.ExpiresAt, &link.UsedAt, &link.UsedBy, &link.CreatedAt)
	if err != nil {
		return nil, err
	}
	link.Token = token
	return &link, nil
}

// GetValidInvitationLinkByHash returns a non-expired invitation link matching
// the given token hash if it still has remaining capacity. Returns (nil, nil)
// when no usable link exists. Validates two flavours:
//   - Platform (project_id IS NULL): rejects if used_at IS NOT NULL.
//   - Project  (project_id NOT NULL): rejects if max_uses != NULL and the
//     count of rows in invitation_link_uses for this link has hit max_uses.
func (s *Store) GetValidInvitationLinkByHash(ctx context.Context, tokenHash string, now time.Time) (*InvitationLink, error) {
	var link InvitationLink
	var useCount int
	err := s.db.QueryRow(ctx, `
		SELECT l.id, l.token_hash, l.invited_by, l.project_id, l.max_uses,
		       l.expires_at, l.used_at, l.used_by, l.created_at,
		       COALESCE((SELECT COUNT(*) FROM invitation_link_uses u WHERE u.link_id = l.id), 0)
		FROM invitation_links l
		WHERE l.token_hash = $1
		  AND (l.expires_at IS NULL OR l.expires_at > $2)
		  AND (
		    (l.project_id IS NULL AND l.used_at IS NULL)
		    OR
		    (l.project_id IS NOT NULL
		      AND (l.max_uses IS NULL
		           OR (SELECT COUNT(*) FROM invitation_link_uses u WHERE u.link_id = l.id) < l.max_uses))
		  )
	`, tokenHash, now).Scan(
		&link.ID, &link.TokenHash, &link.InvitedBy, &link.ProjectID, &link.MaxUses,
		&link.ExpiresAt, &link.UsedAt, &link.UsedBy, &link.CreatedAt, &useCount)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	link.UseCount = useCount
	return &link, nil
}

// ConsumeInvitationLink atomically marks a PLATFORM-scoped (single-use) link
// as used by the given user. Returns an error if the link was already
// consumed in a concurrent request, or if it is a project-scoped link (the
// caller should use RecordInvitationLinkUse instead).
func (s *Store) ConsumeInvitationLink(ctx context.Context, linkID, userID uuid.UUID) error {
	res, err := s.db.Exec(ctx, `
		UPDATE invitation_links
		SET used_at = NOW(), used_by = $1
		WHERE id = $2 AND used_at IS NULL AND project_id IS NULL
	`, userID, linkID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("invitation link already consumed")
	}
	return nil
}

// RecordInvitationLinkUse records one consumption of a project-scoped
// invitation link by userID. Idempotent: the UNIQUE(link_id,user_id)
// constraint silently absorbs repeat clicks from the same user. Callers
// should additionally upsert (project_id, user_id) into project_access_users.
func (s *Store) RecordInvitationLinkUse(ctx context.Context, linkID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO invitation_link_uses (id, link_id, user_id, used_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (link_id, user_id) DO NOTHING
	`, uuid.New(), linkID, userID)
	return err
}

// ListInvitationLinks returns the platform-scoped (project_id IS NULL) links
// used by the admin invitations page. Project-scoped links are excluded so
// they don't leak across projects in the admin view.
func (s *Store) ListInvitationLinks(ctx context.Context) ([]*InvitationLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT l.id, l.token_hash, l.invited_by, l.project_id, l.max_uses,
		       l.expires_at, l.used_at, l.used_by, l.created_at,
		       COALESCE(inv.name, ''), COALESCE(inv.email, ''),
		       COALESCE(used.email, '')
		FROM invitation_links l
		LEFT JOIN users inv  ON inv.id  = l.invited_by
		LEFT JOIN users used ON used.id = l.used_by
		WHERE l.project_id IS NULL
		ORDER BY l.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*InvitationLink, 0)
	for rows.Next() {
		var link InvitationLink
		if err := rows.Scan(&link.ID, &link.TokenHash, &link.InvitedBy,
			&link.ProjectID, &link.MaxUses, &link.ExpiresAt, &link.UsedAt, &link.UsedBy, &link.CreatedAt,
			&link.InvitedByName, &link.InvitedByEmail, &link.UsedByEmail); err != nil {
			return nil, err
		}
		out = append(out, &link)
	}
	return out, nil
}

// ListProjectInvitationLinks returns all invitation links for one project,
// newest first. UseCount is populated for each row.
func (s *Store) ListProjectInvitationLinks(ctx context.Context, projectID uuid.UUID) ([]*InvitationLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT l.id, l.token_hash, l.invited_by, l.project_id, l.max_uses,
		       l.expires_at, l.used_at, l.used_by, l.created_at,
		       COALESCE(inv.name, ''), COALESCE(inv.email, ''),
		       COALESCE((SELECT COUNT(*) FROM invitation_link_uses u WHERE u.link_id = l.id), 0)
		FROM invitation_links l
		LEFT JOIN users inv ON inv.id = l.invited_by
		WHERE l.project_id = $1
		ORDER BY l.created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*InvitationLink, 0)
	for rows.Next() {
		var link InvitationLink
		if err := rows.Scan(&link.ID, &link.TokenHash, &link.InvitedBy,
			&link.ProjectID, &link.MaxUses, &link.ExpiresAt, &link.UsedAt, &link.UsedBy, &link.CreatedAt,
			&link.InvitedByName, &link.InvitedByEmail, &link.UseCount); err != nil {
			return nil, err
		}
		out = append(out, &link)
	}
	return out, nil
}

// ListInvitationLinkUses returns the consumption history for one link with
// each consumer's display fields, newest first.
func (s *Store) ListInvitationLinkUses(ctx context.Context, linkID uuid.UUID) ([]*InvitationLinkUse, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.link_id, u.user_id, u.used_at,
		       COALESCE(usr.email, ''), COALESCE(usr.name, ''), COALESCE(usr.avatar_url, '')
		FROM invitation_link_uses u
		LEFT JOIN users usr ON usr.id = u.user_id
		WHERE u.link_id = $1
		ORDER BY u.used_at DESC
	`, linkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*InvitationLinkUse, 0)
	for rows.Next() {
		var use InvitationLinkUse
		if err := rows.Scan(&use.ID, &use.LinkID, &use.UserID, &use.UsedAt,
			&use.UserEmail, &use.UserName, &use.AvatarURL); err != nil {
			return nil, err
		}
		out = append(out, &use)
	}
	return out, nil
}

// GetInvitationLink returns a single link by id (no project scoping). Callers
// that need to enforce per-project access should check link.ProjectID.
func (s *Store) GetInvitationLink(ctx context.Context, id uuid.UUID) (*InvitationLink, error) {
	var link InvitationLink
	err := s.db.QueryRow(ctx, `
		SELECT id, token_hash, invited_by, project_id, max_uses,
		       expires_at, used_at, used_by, created_at
		FROM invitation_links WHERE id = $1
	`, id).Scan(&link.ID, &link.TokenHash, &link.InvitedBy, &link.ProjectID, &link.MaxUses,
		&link.ExpiresAt, &link.UsedAt, &link.UsedBy, &link.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &link, err
}

func (s *Store) DeleteInvitationLink(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM invitation_links WHERE id = $1`, id)
	return err
}

// AddProjectAlias inserts a custom-domain alias for the given project. The
// caller is responsible for normalising and validating host shape; the DB
// CHECK enforces lowercase and UNIQUE catches cross-project collisions.
func (s *Store) AddProjectAlias(ctx context.Context, projectID uuid.UUID, host string) (*ProjectAlias, error) {
	var a ProjectAlias
	err := s.db.QueryRow(ctx, `
		INSERT INTO project_aliases (id, project_id, host, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING id, project_id, host, created_at
	`, uuid.New(), projectID, host).Scan(&a.ID, &a.ProjectID, &a.Host, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// RemoveProjectAlias deletes one alias by id. Returns nil even when no row
// matched (idempotent for repeat clicks).
func (s *Store) RemoveProjectAlias(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM project_aliases WHERE id = $1`, id)
	return err
}

// ListProjectAliasesByProject returns the aliases attached to one project,
// newest first.
func (s *Store) ListProjectAliasesByProject(ctx context.Context, projectID uuid.UUID) ([]*ProjectAlias, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, host, created_at
		FROM project_aliases
		WHERE project_id = $1
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ProjectAlias, 0)
	for rows.Next() {
		var a ProjectAlias
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Host, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, nil
}

// ListAllProjectAliases returns every alias paired with its owning project's
// running-deployment-relevant fields. Used by the Traefik config generator to
// emit one router per alias alongside the default `<prefix>.<base_domain>`
// router, so callers don't need to issue N round-trips per deployment.
func (s *Store) ListAllProjectAliases(ctx context.Context) ([]*ProjectAlias, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, project_id, host, created_at
		FROM project_aliases
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ProjectAlias, 0)
	for rows.Next() {
		var a ProjectAlias
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Host, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, nil
}

// GetProjectAlias returns one alias by id (no project scoping). The caller
// should check ProjectID against the owning project before mutating.
func (s *Store) GetProjectAlias(ctx context.Context, id uuid.UUID) (*ProjectAlias, error) {
	var a ProjectAlias
	err := s.db.QueryRow(ctx, `
		SELECT id, project_id, host, created_at
		FROM project_aliases WHERE id = $1
	`, id).Scan(&a.ID, &a.ProjectID, &a.Host, &a.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetProjectByAliasHost resolves a project by one of its alias hosts. Returns
// (nil, nil) when no alias matches. The DB stores host lowercased, so the
// caller must pass lowercase too.
func (s *Store) GetProjectByAliasHost(ctx context.Context, host string) (*Project, error) {
	var p Project
	row := s.db.QueryRow(ctx, `
		SELECT `+projectColumnsPrefixed+`
		FROM projects p
		JOIN project_aliases a ON a.project_id = p.id
		WHERE a.host = $1
	`, host)
	err := scanProject(row, &p)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
