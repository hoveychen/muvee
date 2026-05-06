-- Project-wise downstream service access control.
-- access_mode='public' (default) keeps existing behaviour: any authenticated
-- muvee user can reach the deployed service. access_mode='private' restricts
-- access to: project owner, system admins, and users listed in
-- project_access_users. The Traefik ForwardAuth /verify endpoint enforces
-- this by calling muvee-server's /api/internal/access/check.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS access_mode TEXT NOT NULL DEFAULT 'public'
    CHECK (access_mode IN ('public', 'private'));

CREATE TABLE IF NOT EXISTS project_access_users (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_access_users_user ON project_access_users(user_id);
