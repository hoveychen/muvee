-- Per-project password ("demo") accounts for the downstream sign-in page.
-- Accounts are provisioned by platform members in the project's Auth tab --
-- there is deliberately NO self-registration path. The downstream login page
-- shows a username/password form whenever a project has at least one enabled
-- account; authservice verifies the bcrypt hash through an internal endpoint
-- and the resulting identity binds to users via oauth_accounts with
-- provider='password' and provider_user_id=<account id>, so demo users get a
-- stable identity without an email, same as social providers.

CREATE TABLE IF NOT EXISTS project_password_accounts (
    id            UUID PRIMARY KEY,
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    username      TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    disabled      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, username)
);

CREATE INDEX IF NOT EXISTS idx_password_accounts_project ON project_password_accounts(project_id);
