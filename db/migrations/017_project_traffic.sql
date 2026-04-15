CREATE TABLE project_traffic (
    id           UUID         PRIMARY KEY,
    project_id   UUID         NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    observed_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    client_ip    TEXT         NOT NULL DEFAULT '',
    host         TEXT         NOT NULL DEFAULT '',
    method       TEXT         NOT NULL DEFAULT '',
    path         TEXT         NOT NULL DEFAULT '',
    status       INTEGER      NOT NULL DEFAULT 0,
    duration_ms  BIGINT       NOT NULL DEFAULT 0,
    bytes_sent   BIGINT       NOT NULL DEFAULT 0,
    user_agent   TEXT         NOT NULL DEFAULT '',
    referer      TEXT         NOT NULL DEFAULT ''
);

CREATE INDEX project_traffic_project_time ON project_traffic (project_id, observed_at DESC);
