-- 001_initial.sql
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL DEFAULT '',
    avatar_url  TEXT NOT NULL DEFAULT '',
    role        TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE projects (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  TEXT NOT NULL,
    git_url               TEXT NOT NULL,
    git_branch            TEXT NOT NULL DEFAULT 'main',
    domain_prefix         TEXT NOT NULL UNIQUE,
    dockerfile_path       TEXT NOT NULL DEFAULT 'Dockerfile',
    owner_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    auth_required         BOOLEAN NOT NULL DEFAULT FALSE,
    auth_allowed_domains  TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE project_members (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, user_id)
);

CREATE TABLE datasets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    nfs_path    TEXT NOT NULL,
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    checksum    TEXT NOT NULL DEFAULT '',
    version     BIGINT NOT NULL DEFAULT 1,
    owner_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE dataset_members (
    dataset_id  UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (dataset_id, user_id)
);

CREATE TABLE project_datasets (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    dataset_id  UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    mount_mode  TEXT NOT NULL DEFAULT 'dependency' CHECK (mount_mode IN ('dependency', 'readwrite')),
    PRIMARY KEY (project_id, dataset_id)
);

CREATE TABLE nodes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hostname            TEXT NOT NULL UNIQUE,
    role                TEXT NOT NULL CHECK (role IN ('builder', 'deploy')),
    max_storage_bytes   BIGINT NOT NULL DEFAULT 0,
    used_storage_bytes  BIGINT NOT NULL DEFAULT 0,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE node_datasets (
    node_id     UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    dataset_id  UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    size_bytes  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (node_id, dataset_id)
);

CREATE TABLE deployments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    image_tag   TEXT NOT NULL DEFAULT '',
    commit_sha  TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','building','deploying','running','failed','stopped')),
    node_id     UUID REFERENCES nodes(id) ON DELETE SET NULL,
    logs        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL CHECK (type IN ('build', 'deploy')),
    node_id         UUID REFERENCES nodes(id) ON DELETE SET NULL,
    deployment_id   UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    payload         JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed')),
    result          TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE dataset_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id      UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    scanned_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    total_files     BIGINT NOT NULL DEFAULT 0,
    total_size_bytes BIGINT NOT NULL DEFAULT 0,
    version         BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE dataset_file_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id      UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    file_path       TEXT NOT NULL,
    event_type      TEXT NOT NULL CHECK (event_type IN ('added', 'modified', 'deleted')),
    old_size        BIGINT NOT NULL DEFAULT 0,
    new_size        BIGINT NOT NULL DEFAULT 0,
    old_checksum    TEXT NOT NULL DEFAULT '',
    new_checksum    TEXT NOT NULL DEFAULT '',
    snapshot_id     UUID NOT NULL REFERENCES dataset_snapshots(id) ON DELETE CASCADE,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_project_members_user ON project_members(user_id);
CREATE INDEX idx_dataset_members_user ON dataset_members(user_id);
CREATE INDEX idx_deployments_project ON deployments(project_id, created_at DESC);
CREATE INDEX idx_tasks_node_status ON tasks(node_id, status);
CREATE INDEX idx_node_datasets_node ON node_datasets(node_id, last_used_at);
CREATE INDEX idx_dataset_snapshots_dataset ON dataset_snapshots(dataset_id, scanned_at DESC);
CREATE INDEX idx_file_history_dataset_path ON dataset_file_history(dataset_id, file_path, occurred_at DESC);
CREATE INDEX idx_file_history_dataset_time ON dataset_file_history(dataset_id, occurred_at DESC);
