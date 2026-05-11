-- Social OAuth identity binding table. Maps each external
-- (provider, provider_user_id) tuple onto a local users.id so a social login
-- that does not surface an email (Discord, Twitter free tier, Apple
-- Hide-My-Email never-shared) can still resolve to a stable muvee user.
--
-- The composite primary key prevents a given provider from re-binding the
-- same external id to a second user_id, but a single user_id MAY have
-- multiple rows in this table -- one row per provider the user has linked.
-- The current EnsureIdentity path only inserts on first sign-in, so until
-- account-merging is wired up each user_id has exactly one binding.
--
-- Note: this table coexists with the users.email path -- platform-side
-- providers (Google / Feishu / WeCom / DingTalk) keep using
-- UpsertUser-by-email and do not write to oauth_accounts. Social providers
-- (Discord / Apple / Facebook / Twitter) write here.

CREATE TABLE IF NOT EXISTS oauth_accounts (
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, provider_user_id)
);

CREATE INDEX IF NOT EXISTS idx_oauth_accounts_user ON oauth_accounts(user_id);
