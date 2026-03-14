-- 005_project_secrets_git_username.sql
-- Add git_username to project_secrets for HTTPS credential injection.
-- When use_for_git=true and the secret type is 'password', the builder rewrites
-- the git URL as https://git_username:TOKEN@host/... (GitHub fine-grained PAT workflow).
ALTER TABLE project_secrets ADD COLUMN git_username TEXT NOT NULL DEFAULT '';
