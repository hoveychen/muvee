-- 053: Project secrets own their value instead of referencing a personal secret.
--
-- Before: project_secrets was a pure binding (project_id, secret_id) pointing at
-- a row in `secrets`, which belongs to one user. The project page could only
-- render bindings whose secret the *viewer* owned, so any member or admin other
-- than the owner saw an empty tab, and non-owners got 403 whenever they saved
-- because the replace-all PUT re-submitted the owner's secret ids.
--
-- After: each project_secrets row carries its own name / type / encrypted_value
-- and a surrogate id. Personal secrets are only a copy source: source_secret_id
-- records where a value was copied from, and is nulled (not cascaded) when that
-- personal secret is deleted, so deleting or rotating a personal secret no
-- longer silently changes a project's deployments.
--
-- Existing bindings are materialised by copying the referenced secret's
-- name / type / encrypted_value, so running deployments keep the same values.

ALTER TABLE project_secrets
    ADD COLUMN id              UUID        NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN name            TEXT        NOT NULL DEFAULT '',
    ADD COLUMN type            TEXT        NOT NULL DEFAULT 'password',
    ADD COLUMN encrypted_value TEXT        NOT NULL DEFAULT '',
    ADD COLUMN created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE project_secrets ps
SET name = s.name,
    type = s.type,
    encrypted_value = s.encrypted_value,
    created_at = s.created_at,
    updated_at = s.updated_at
FROM secrets s
WHERE s.id = ps.secret_id;

ALTER TABLE project_secrets DROP CONSTRAINT project_secrets_pkey;
ALTER TABLE project_secrets DROP CONSTRAINT project_secrets_secret_id_fkey;
ALTER TABLE project_secrets RENAME COLUMN secret_id TO source_secret_id;
ALTER TABLE project_secrets ALTER COLUMN source_secret_id DROP NOT NULL;
ALTER TABLE project_secrets
    ADD CONSTRAINT project_secrets_source_secret_id_fkey
    FOREIGN KEY (source_secret_id) REFERENCES secrets(id) ON DELETE SET NULL;
ALTER TABLE project_secrets ADD PRIMARY KEY (id);
ALTER TABLE project_secrets
    ADD CONSTRAINT project_secrets_type_check
    CHECK (type IN ('password', 'ssh_key', 'api_key', 'env_var', 'registry'));

ALTER INDEX idx_project_secrets_secret RENAME TO idx_project_secrets_source;
