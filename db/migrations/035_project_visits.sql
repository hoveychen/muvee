-- Per-project unique-visitor counter, used to give project owners visibility
-- into who has been hitting their downstream service (regardless of public
-- or private access_mode). Updated by an in-process batch worker on every
-- ForwardAuth allow decision (see internal/api/visit_recorder.go).
--
-- One row per (project, user) pair: visit_count counts ForwardAuth verify
-- hits since the row was created, last_seen_at is the most recent hit, and
-- first_seen_at is the row creation time. This is *not* a per-request audit
-- log — long-form access auditing belongs in a separate table if ever
-- needed.

CREATE TABLE IF NOT EXISTS project_visits (
    project_id     UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id        UUID        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    visit_count    BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_visits_project_lastseen
    ON project_visits(project_id, last_seen_at DESC);
