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
  domain_prefix: string
  dockerfile_path: string
  owner_id: string
  auth_required: boolean
  auth_allowed_domains: string
  created_at: string
  updated_at: string
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
}

export interface DatasetSnapshot {
  id: string
  dataset_id: string
  scanned_at: string
  total_files: number
  total_size_bytes: number
  version: number
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
