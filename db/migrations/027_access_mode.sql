-- 027: Replace the require_authorization boolean with a three-state access_mode
-- setting (open / invite / request) and add the tables needed for invite-only
-- access (email white-list + single-use invitation links).
--
-- access_mode meanings:
--   open    : any user passing the OAuth/domain check is auto-authorized.
--   invite  : only emails listed in `invitations` (or holders of a valid
--             invitation_link token) can sign in. Others get rejected.
--   request : new users are created with authorized=FALSE and must request
--             access via the existing authorization_requests flow.

-- Seed access_mode from any existing require_authorization value:
--   require_authorization = 'true'  -> 'request'
--   require_authorization = 'false' / missing -> 'open'
INSERT INTO system_settings (key, value) VALUES (
    'access_mode',
    COALESCE(
        (SELECT CASE WHEN value = 'true' THEN 'request' ELSE 'open' END
         FROM system_settings WHERE key = 'require_authorization'),
        'open'
    )
) ON CONFLICT (key) DO NOTHING;

-- Drop the old boolean setting; access_mode is the source of truth from now on.
DELETE FROM system_settings WHERE key = 'require_authorization';

-- Email white-list: admins pre-approve email addresses that may sign in.
-- Email is stored normalized (lowercase, trimmed) and is the unique key.
CREATE TABLE IF NOT EXISTS invitations (
    id          UUID        PRIMARY KEY,
    email       TEXT        NOT NULL UNIQUE,
    invited_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_invitations_email ON invitations(email);

-- Single-use invitation links: admin generates a token URL, the first OAuth
-- login carrying the token is auto-authorized and the link is consumed.
CREATE TABLE IF NOT EXISTS invitation_links (
    id          UUID        PRIMARY KEY,
    token_hash  TEXT        NOT NULL UNIQUE,    -- sha256(token)
    invited_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    expires_at  TIMESTAMPTZ,                    -- NULL = never expires
    used_at     TIMESTAMPTZ,                    -- NULL = unused
    used_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_invitation_links_token_hash ON invitation_links(token_hash);
