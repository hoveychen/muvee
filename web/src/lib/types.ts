export interface User {
  id: string
  email: string
  name: string
  avatar_url: string
  role: 'admin' | 'member'
  authorized: boolean
  created_at: string
  name_overridden: boolean
  avatar_overridden: boolean
}

export interface Project {
  id: string
  name: string
  project_type: 'deployment' | 'domain_only' | 'compose' | 'image'
  git_url: string
  git_branch: string
  git_source: 'external' | 'hosted'
  git_push_url?: string
  domain_prefix: string
  dockerfile_path: string
  owner_id: string
  owner_name?: string
  owner_email?: string
  owner_avatar_url?: string
  auth_required: boolean
  auth_allowed_domains: string
  auth_bypass_paths: string
  memory_limit: string
  volume_mount_path: string
  description: string
  icon: string
  tags: string
  // Compose-specific fields (only meaningful when project_type === 'compose').
  compose_file_path?: string
  expose_service?: string
  expose_port?: number
  pinned_node_id?: string | null
  // container_port is required by all deploying project types; for 'image'
  // projects it's the port published by the pre-built image.
  container_port?: number
  // Image-specific field (only meaningful when project_type === 'image').
  image_ref?: string
  // Auto-deploy: when true, the server triggers a fresh deployment whenever
  // the tracked branch advances (push for hosted repos, poll for external).
  auto_deploy_enabled: boolean
  // last_tracked_commit_sha is server-managed (read-only from the client).
  last_tracked_commit_sha: string
  // last_tracked_image_digests is a JSON-encoded map (image -> digest) the
  // image-digest watcher uses to detect upstream image updates for compose
  // projects. Server-managed, read-only.
  last_tracked_image_digests: string
  // access_mode controls who can reach the deployed downstream service:
  //   'public'  — any authenticated muvee user (default, legacy behaviour)
  //   'private' — only project owner, system admins, and explicitly allowed users
  access_mode: 'public' | 'private'
  // Comma-separated whitelist of OAuth provider names this project's
  // downstream sign-in flow may use (e.g. "google,feishu"). Empty = inherit
  // the globally-configured set.
  enabled_providers: string
  // Admin-only fixed-port binding. When both are set, the project is forced
  // onto fixed_node_id and its container's port is published on
  // fixed_host_port (instead of an ephemeral host port). null/undefined means
  // dynamic allocation (default).
  fixed_host_port?: number | null
  fixed_node_id?: string | null
  created_at: string
  updated_at: string
}

export interface ProjectAccessUser {
  project_id: string
  user_id: string
  added_by?: string
  added_at: string
  email: string
  name: string
  avatar_url: string
}

export interface ProjectVisit {
  project_id: string
  user_id: string
  first_seen_at: string
  last_seen_at: string
  visit_count: number
  email: string
  name: string
  avatar_url: string
  in_allow_list: boolean
}

// Minimal project projection returned by GET /api/projects/{id}/info. Used
// by the request-access page so users who are not yet members can render
// the project name without tripping the regular getProject 404.
export interface ProjectInfo {
  id: string
  name: string
  access_mode: 'public' | 'private'
  owner_name: string
  owner_email: string
}

export type ProjectAccessRequestStatus = 'pending' | 'approved' | 'denied'

export interface ProjectAccessRequest {
  id: string
  project_id: string
  user_id: string
  reason: string
  status: ProjectAccessRequestStatus
  requested_at: string
  decided_at?: string
  decided_by?: string
  user_email?: string
  user_name?: string
  user_avatar_url?: string
  project_name?: string
  project_domain_prefix?: string
}

export interface RepoTreeEntry {
  name: string
  type: 'blob' | 'tree'
  size: number
  path: string
}

export interface RepoCommit {
  sha: string
  message: string
  author: string
  date: string
}

export interface RepoBranch {
  name: string
  is_default: boolean
}

export interface WorkspaceEntry {
  name: string
  size: number
  is_dir: boolean
  mod_time: number
}

export interface Dataset {
  id: string
  name: string
  nfs_path: string
  size_bytes: number
  checksum: string
  version: number
  owner_id: string
  created_at: string
  updated_at: string
}

export interface RuntimeConfig {
  dataset_nfs_base_path: string
  base_domain: string
  secrets_enabled: boolean
  server_version: string
}

export interface ProjectDataset {
  project_id: string
  dataset_id: string
  mount_mode: 'dependency' | 'readwrite'
}

export interface Deployment {
  id: string
  project_id: string
  image_tag: string
  commit_sha: string
  status: 'pending' | 'building' | 'deploying' | 'running' | 'failed' | 'stopped'
  node_id: string | null
  logs: string
  restart_count: number
  oom_killed: boolean
  created_at: string
  updated_at: string
}

export interface Node {
  id: string
  hostname: string
  role: 'builder' | 'deploy'
  max_storage_bytes: number
  used_storage_bytes: number
  last_seen_at: string
  created_at: string
  // health_report is the latest self-reported health checks from the agent (JSON bytes, base64-encoded).
  health_report?: string | null
}

export interface DatasetSnapshot {
  id: string
  dataset_id: string
  scanned_at: string
  total_files: number
  total_size_bytes: number
  version: number
}

export interface ApiToken {
  id: string
  name: string
  last_used_at: string | null
  expires_at?: string | null
  created_at: string
}

export interface CreatedApiToken {
  id: string
  name: string
  token: string
  expires_at?: string
}

export type SecretType = 'password' | 'ssh_key' | 'api_key' | 'env_var'

export interface Secret {
  id: string
  name: string
  type: SecretType
  // value_preview is a non-sensitive display string:
  // - api_key: masked fingerprint like "sk-1****wxyz"
  // - env_var: full plaintext value
  // - password / ssh_key: empty string
  value_preview: string
  created_at: string
  updated_at: string
}

export interface ProjectSecretBinding {
  secret_id: string
  secret_name: string
  secret_type: SecretType
  env_var_name: string
  use_for_git: boolean
  use_for_build: boolean
  build_secret_id: string
  // git_username is used with password-type secrets for HTTPS git authentication.
  // e.g. "x-access-token" for GitHub fine-grained PATs.
  git_username: string
}

export interface NodeMetric {
  node_id: string
  collected_at: number // epoch seconds
  cpu_percent: number
  mem_total_bytes: number
  mem_used_bytes: number
  disk_total_bytes: number
  disk_used_bytes: number
  load1: number
  load5: number
  load15: number
}

export interface ContainerMetric {
  deployment_id: string
  collected_at: number // epoch seconds
  cpu_percent: number
  mem_usage_bytes: number
  mem_limit_bytes: number
  net_rx_bytes: number
  net_tx_bytes: number
  block_read_bytes: number
  block_write_bytes: number
}

export interface ProjectTraffic {
  observed_at: number // epoch seconds
  client_ip: string
  host: string
  method: string
  path: string
  status: number
  duration_ms: number
  bytes_sent: number
  user_agent: string
  referer: string
}

export type AccessMode = 'open' | 'invite' | 'request'

export interface SystemSettings {
  onboarded: string       // 'true' | 'false'
  site_name: string
  logo_url: string
  favicon_url: string
  // 'open' (anyone in the org), 'invite' (white-list), 'request' (request-access flow).
  access_mode: AccessMode | ''
  // ─── Social OAuth providers (downstream ForwardAuth only) ────────────
  // Configured at runtime via /admin/settings. Empty string = not set;
  // *_enabled is the string 'true' | 'false' to mirror the kv store wire
  // format. ApplyChanges triggers a live reload of muvee-authservice's
  // provider set.
  //
  // google_* is a downstream-only Google OAuth app, distinct from the
  // platform-side env GOOGLE_CLIENT_ID. When unset, downstream falls
  // back to the env-configured Google app.
  google_enabled?: string
  google_client_id?: string
  google_client_secret?: string
  google_redirect_url?: string
  discord_enabled?: string
  discord_client_id?: string
  discord_client_secret?: string
  discord_redirect_url?: string
  facebook_enabled?: string
  facebook_client_id?: string
  facebook_client_secret?: string
  facebook_redirect_url?: string
  twitter_enabled?: string
  twitter_client_id?: string
  twitter_client_secret?: string
  twitter_redirect_url?: string
  apple_enabled?: string
  apple_client_id?: string
  apple_team_id?: string
  apple_key_id?: string
  apple_private_key_p8?: string
  apple_redirect_url?: string
}

export interface AuthorizationRequest {
  id: string
  user_id: string
  status: 'pending' | 'approved' | 'rejected'
  reviewed_by: string | null
  created_at: string
  updated_at: string
  user_name?: string
  user_email?: string
  user_avatar_url?: string
}

export interface AuthorizationStatus {
  access_mode: AccessMode
  authorized: boolean
  request?: AuthorizationRequest | null
}

export interface Invitation {
  id: string
  email: string
  invited_by?: string | null
  invited_by_name?: string
  invited_by_email?: string
  created_at: string
}

export interface InvitationLink {
  id: string
  invited_by?: string | null
  invited_by_name?: string
  invited_by_email?: string
  expires_at?: string | null
  used_at?: string | null
  used_by?: string | null
  used_by_email?: string
  created_at: string
  // Token is only present in the response from POST /api/admin/invitation-links
  // (one-time return — never recoverable afterwards).
  token?: string
}

export type HealthStatus = 'ok' | 'warning' | 'error'

export interface HealthCheck {
  name: string
  status: HealthStatus
  message: string
  hint?: string
}

export interface HealthReport {
  checks: HealthCheck[]
  updated_at: string
}

export type CertKind = 'base' | 'registry' | 'traefik' | 'project' | 'tunnel'
export type CertStatusKind = 'issued' | 'pending' | 'unknown'

export interface CertStatus {
  domain: string
  kind: CertKind
  status: CertStatusKind
  not_after?: string
  days_left?: number
  issuer?: string
  message?: string
}

export interface CertReport {
  store_path: string
  store_error?: string
  items: CertStatus[]
  updated_at: string
}

export interface ActiveTunnel {
  domain: string
  user_email: string
  auth_required: boolean
  connected_at: string
  project_name: string
}

export interface TunnelHistoryEntry {
  id: string
  domain: string
  user_email: string
  auth_required: boolean
  connected_at: string
  disconnected_at: string | null
}

export interface FileHistory {
  id: string
  dataset_id: string
  file_path: string
  event_type: 'added' | 'modified' | 'deleted'
  old_size: number
  new_size: number
  old_checksum: string
  new_checksum: string
  snapshot_id: string
  occurred_at: string
}
