-- 044_secret_registry_creds.sql
-- Per-tenant private registry pull credentials.
--
-- A secret of type 'registry' holds a registry login: registry_addr is the
-- registry host (e.g. ghcr.io), registry_username the login user, and the
-- encrypted value the password / token. At deploy time the scheduler injects
-- every registry secret owned by a project's owner so the deploy agent can pull
-- private compose images. Non-registry secrets leave both columns empty.
ALTER TABLE secrets ADD COLUMN IF NOT EXISTS registry_addr TEXT NOT NULL DEFAULT '';
ALTER TABLE secrets ADD COLUMN IF NOT EXISTS registry_username TEXT NOT NULL DEFAULT '';

ALTER TABLE secrets DROP CONSTRAINT secrets_type_check;
ALTER TABLE secrets ADD CONSTRAINT secrets_type_check
    CHECK (type IN ('password', 'ssh_key', 'api_key', 'env_var', 'registry'));
