-- Demo accounts need an email so downstream services get a populated
-- X-Forwarded-User header (the authservice forward JWT carries no email for
-- password logins otherwise -- see cmd/muvee/authservice.go). The email is a
-- pure passthrough attribute: it is baked into the forward JWT and handed to
-- the downstream app, but access control still runs off the per-project
-- account list + ProjectID binding, NOT an email-keyed ACL. It is therefore
-- deliberately NOT unique -- username stays the sole per-project login key.
--
-- NOT NULL DEFAULT '' keeps any pre-existing rows valid; the create/update API
-- enforces a non-empty, well-formed email for new and edited accounts.
ALTER TABLE project_password_accounts
    ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT '';
