-- Per-session revocation for project-subdomain credentials
-- (BACKLOG-20261117-007). Subsite sessions, the central SSO session on each
-- base-domain apex, and CLI device-flow tokens are stateless JWTs that each
-- carry a random session id (sid; subsite sessions also carry their SSO's
-- sso_sid). Signing out of one subsite inserts that session's sid and its
-- sso_sid here; authservice rejects any credential whose sid or sso_sid is
-- listed, on every request path.
--
-- expires_at bounds how long a row matters: it is set to the revocation time
-- plus the longest lifetime of any credential carrying that sid (the SSO
-- session's 7 days; a revoked credential can never be renewed, so nothing
-- outlives that). Expired rows are ignored by readers and deleted
-- opportunistically on insert.
--
-- Rollback: only code that knows about the table reads it; older images ignore
-- it. To drop it: DROP TABLE fwd_session_revocations;
-- DELETE FROM schema_migrations WHERE filename = '058_fwd_session_revocations.sql';

CREATE TABLE IF NOT EXISTS fwd_session_revocations (
    sid        TEXT        PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS fwd_session_revocations_expires_at_idx
    ON fwd_session_revocations (expires_at);
