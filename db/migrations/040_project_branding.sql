-- Per-project branding for the forward-auth login page served on each
-- project's downstream subdomain. Without these columns the Go template at
-- cmd/muvee/authservice.go renders a generic indigo/grey card that looks
-- nothing like the platform's own React Login.tsx; downstream end-users hit
-- that page after being redirected by the auth gate and have no way to tell
-- they are signing in to "their" product.
--
-- Empty string = inherit the platform-wide defaults (system_settings.site_name,
-- system_settings.logo_url, hard-coded sidebar / accent colours). The
-- ForwardAuth render path falls back in that order: project -> platform ->
-- built-in. All six columns are TEXT NOT NULL DEFAULT '' so existing projects
-- need no backfill and stay on the inherited theme until an owner customises.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_site_name      TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_logo_url       TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_favicon_url    TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_primary_color  TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_sidebar_bg     TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_tagline        TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_description    TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_footer_text    TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branding_trust_text     TEXT NOT NULL DEFAULT '';
