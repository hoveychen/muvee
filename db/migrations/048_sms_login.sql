-- Phone / SMS one-time-code login for the downstream ForwardAuth sign-in page.
--
-- Unlike password ("demo") accounts, which are provisioned by platform members
-- and have NO self-registration, SMS login is deliberately SELF-SERVICE: anyone
-- who can receive the code at a phone number signs in, and a stable identity is
-- created on first login via oauth_accounts with provider='phone' and
-- provider_user_id=<E.164 phone> (same identity mechanism as social/password).
-- Because that is a much wider door than the demo-account whitelist, SMS login
-- is OFF by default per project and must be explicitly enabled by the owner in
-- the Auth tab -- hence the sms_login_enabled flag below, kept orthogonal to
-- enabled_providers (OAuth) and to the implicit password_login flag.
--
-- Verification codes live in Postgres (no Redis in this deployment). Only the
-- sha256 hash of the code is stored, never the plaintext. Rate limiting and
-- per-code attempt caps run off this table (see internal/api/sms_login.go):
-- the (phone, project_id, created_at DESC) index backs both "latest unconsumed
-- code" lookups and the resend / daily-cap counting queries.

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS sms_login_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS sms_verification_codes (
    id          UUID PRIMARY KEY,
    phone       TEXT NOT NULL,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    code_hash   TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sms_codes_phone_project_created
    ON sms_verification_codes(phone, project_id, created_at DESC);
