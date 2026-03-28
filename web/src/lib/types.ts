export interface User {
  id: string
  email: string
  name: string
  avatar_url: string
  role: 'admin' | 'member'
  created_at: string
}

export interface Project {
  id: string
  name: string
  git_url: string
  git_branch: string
  git_source: 'external' | 'hosted'
  git_push_url?: string
  domain_prefix: string
  dockerfile_path: string
  owner_id: string
  auth_required: boolean
  auth_allowed_domains: string
  memory_limit: string
  volume_mount_path: string
  created_at: string
  updated_at: string
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
  created_at: string
}

export interface CreatedApiToken {
  id: string
  name: string
  token: string
}

export interface Secret {
  id: string
  name: string
  type: 'password' | 'ssh_key'
  created_at: string
  updated_at: string
}

export interface ProjectSecretBinding {
  secret_id: string
  secret_name: string
  secret_type: 'password' | 'ssh_key'
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

export interface SystemSettings {
  onboarded: string       // 'true' | 'false'
  site_name: string
  logo_url: string
  favicon_url: string
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
