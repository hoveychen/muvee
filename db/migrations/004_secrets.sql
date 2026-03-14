-- 004_secrets.sql
CREATE TABLE secrets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('password', 'ssh_key')),
    encrypted_value TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_secrets_user ON secrets(user_id);

CREATE TABLE project_secrets (
    project_id   UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    secret_id    UUID NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    env_var_name TEXT NOT NULL DEFAULT '',
    use_for_git  BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (project_id, secret_id)
);

CREATE INDEX idx_project_secrets_project ON project_secrets(project_id);
CREATE INDEX idx_project_secrets_secret  ON project_secrets(secret_id);
