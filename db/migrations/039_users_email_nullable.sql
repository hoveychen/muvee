-- Allow users.email to be NULL so social-login users that do not surface an
-- email (Discord, Twitter free tier, Apple Hide-My-Email never-shared) can
-- still land in the users table via oauth_accounts.
--
-- The UNIQUE constraint on email is preserved unchanged: Postgres treats two
-- NULL values as distinct under a UNIQUE index, so any number of
-- email-less rows can coexist without conflict. Non-NULL emails (platform
-- users from Google / Feishu / WeCom / DingTalk) keep their uniqueness
-- guarantee.
--
-- Application-layer responsibilities introduced alongside this migration:
--   1. scanUser tolerates NULL via sql.NullString and surfaces "" to callers.
--   2. INSERT/UPDATE paths convert "" -> NULL via a nullIfEmpty helper so
--      the UNIQUE index does not collapse multiple email-less users onto a
--      single empty-string key.
--   3. UpsertUser (ON CONFLICT (email) DO UPDATE) is NOT used for social
--      logins -- those go through EnsureUserByOAuth which keys on
--      oauth_accounts(provider, provider_user_id) instead.

ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
