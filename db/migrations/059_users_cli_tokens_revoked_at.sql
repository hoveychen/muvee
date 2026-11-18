-- Console logout also voids the user's CLI device-flow tokens, and the console
-- can void them on their own ("revoke all CLI tokens") (BACKLOG-20261117-009).
-- CLI tokens are stateless JWTs bound to one subsite, so they cannot be
-- deleted from the server: instead this column is stamped, and authservice
-- rejects any kind=cli token of that user whose auth_time is at or before it.
--
-- A separate column on purpose: fwd_sessions_revoked_at (057) has been stamped
-- by every console logout since it shipped, so comparing CLI tokens with it
-- would retroactively void tokens issued before a logout that, at the time,
-- did not touch them. NULL (every row after this migration) means no CLI
-- revocation on record: existing tokens keep working until the user's next
-- console logout or a click on the revoke button.
--
-- Rollback: the column is only read by code that knows about it; older images
-- ignore it. To drop it: ALTER TABLE users DROP COLUMN cli_tokens_revoked_at;
-- DELETE FROM schema_migrations WHERE filename = '059_users_cli_tokens_revoked_at.sql';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS cli_tokens_revoked_at TIMESTAMPTZ;
