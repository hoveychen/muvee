-- Console logout signs the user out of every project subdomain too
-- (BACKLOG-20261117-008). Subsite sessions (muvee_fwd_session) are stateless
-- JWTs scoped to a base domain, so they cannot be deleted from the server:
-- instead the console logout stamps this column, and authservice rejects any
-- subsite session of that user issued at or before it — on every device and
-- every base domain. NULL means the user never signed out of the console, so
-- their sessions are unaffected (including ones signed before sessions carried
-- an issued-at time).
--
-- CLI device-flow Bearer tokens are not affected (BACKLOG-20261117-007).
--
-- Rollback: the column is only read by code that knows about it; older images
-- ignore it. To drop it: ALTER TABLE users DROP COLUMN fwd_sessions_revoked_at;
-- DELETE FROM schema_migrations WHERE filename = '057_users_fwd_sessions_revoked_at.sql';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS fwd_sessions_revoked_at TIMESTAMPTZ;
