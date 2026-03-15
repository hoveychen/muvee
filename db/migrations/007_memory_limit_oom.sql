-- Add per-project memory limit for Docker container resource capping.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS memory_limit TEXT NOT NULL DEFAULT '4g';

-- Add runtime health fields to track container restarts and OOM kills.
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS restart_count INT NOT NULL DEFAULT 0;
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS oom_killed BOOL NOT NULL DEFAULT FALSE;
