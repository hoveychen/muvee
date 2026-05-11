-- Per-project OAuth provider whitelist. Empty string = inherit the global
-- enabled set (every provider configured via env vars), preserving the legacy
-- behaviour for all existing projects without backfill. Non-empty = a
-- comma-separated list of provider names (e.g. "google,feishu") that this
-- project's downstream sign-in flow is allowed to use. The SDK and the
-- ForwardAuth login pages both honour this column; authservice also
-- double-checks at OAuth callback time so a stale state cookie cannot bypass
-- a configuration change.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS enabled_providers TEXT NOT NULL DEFAULT '';
