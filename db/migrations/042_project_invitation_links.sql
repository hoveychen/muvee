-- Per-project invitation links: extend the existing invitation_links table so
-- one token can be either a platform-only single-use invite (the original
-- behaviour, project_id IS NULL) or a project-scoped multi-use invite that
-- auto-adds the consumer to project_access_users (project_id NOT NULL).
--
-- max_uses semantics:
--   NULL  = unlimited (typical project link; expires_at / manual revoke gate
--           lifetime instead).
--   N>=1  = link is valid until N distinct consumers have used it.
--   1     = single-use (matches the original platform-invite path; existing
--           rows are interpreted as max_uses=1 via the COALESCE in queries).
--
-- For platform invites (project_id IS NULL) the legacy used_at / used_by
-- columns remain the source of truth. For project invites we never touch
-- used_at; consumption is tracked in invitation_link_uses (one row per
-- (link, user) pair — UNIQUE guarantees idempotent re-clicks).

ALTER TABLE invitation_links
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS max_uses   INT;

CREATE INDEX IF NOT EXISTS idx_invitation_links_project ON invitation_links(project_id);

CREATE TABLE IF NOT EXISTS invitation_link_uses (
    id         UUID        PRIMARY KEY,
    link_id    UUID        NOT NULL REFERENCES invitation_links(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    used_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (link_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_invitation_link_uses_link ON invitation_link_uses(link_id);
CREATE INDEX IF NOT EXISTS idx_invitation_link_uses_user ON invitation_link_uses(user_id);
