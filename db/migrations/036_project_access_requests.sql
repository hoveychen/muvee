-- Access-request inbox for private projects. When ForwardAuth /verify
-- denies a user (access_mode='private', not owner, not in
-- project_access_users), the deny page links them to /request-access on the
-- platform domain, where they submit a row here. The project owner (or a
-- platform admin) decides each request — approve also INSERTs into
-- project_access_users, so the actual grant lives in one place.
--
-- A user may have at most one pending row per project (enforced by the
-- partial unique index below). After a decision, status moves to 'approved'
-- or 'denied' and stays there; if a previously-denied user wants to retry,
-- they create a new row (the partial index lets pending coexist with
-- historical decisions). Approved requests are never reverted here — owner
-- removes them from project_access_users to revoke.

CREATE TABLE IF NOT EXISTS project_access_requests (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id       UUID        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    reason        TEXT        NOT NULL DEFAULT '',
    status        TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'approved', 'denied')),
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at    TIMESTAMPTZ,
    decided_by    UUID        REFERENCES users(id) ON DELETE SET NULL
);

-- One pending request per (project, user). Multiple historical
-- approved/denied rows are allowed; the partial predicate is what keeps
-- pending uniqueness without blocking retries.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_project_access_requests_pending
    ON project_access_requests(project_id, user_id)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_project_access_requests_project_status
    ON project_access_requests(project_id, status);

CREATE INDEX IF NOT EXISTS idx_project_access_requests_user
    ON project_access_requests(user_id);
