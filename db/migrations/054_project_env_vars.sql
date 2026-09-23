-- 054: Project secrets become plain KEY=VALUE environment variables.
--
-- Before (053): each row had a display name, a type (password / api_key /
-- env_var / ssh_key / registry), a separate env_var_name, a separate
-- build_secret_id, and use_for_git / git_username. Users could not tell the
-- secret name from the env var name, and "type" was really only encoding
-- whether the value may be shown.
--
-- After: a row is KEY + encrypted value + three flags:
--   sensitive — value is write-only (was every type except env_var)
--   runtime   — injected into the container env under KEY
--   build     — passed to docker buildx as a build secret with id=KEY
-- Git clone credentials move to dedicated columns on projects, because they
-- are repository settings, not environment variables.
--
-- Rows that were only a git credential are moved and removed. Registry rows are
-- removed: registry credentials always applied per owner, so binding one to a
-- project never had any effect.

ALTER TABLE projects
    ADD COLUMN git_auth_type      TEXT NOT NULL DEFAULT 'none'
        CHECK (git_auth_type IN ('none', 'https_token', 'ssh_key')),
    ADD COLUMN git_auth_username  TEXT NOT NULL DEFAULT '',
    ADD COLUMN git_auth_encrypted TEXT NOT NULL DEFAULT '';

UPDATE projects p
SET git_auth_type      = CASE g.type WHEN 'ssh_key' THEN 'ssh_key' ELSE 'https_token' END,
    git_auth_username  = CASE WHEN g.type = 'ssh_key' THEN ''
                              ELSE COALESCE(NULLIF(g.git_username, ''), 'x-access-token') END,
    git_auth_encrypted = g.encrypted_value
FROM (
    SELECT DISTINCT ON (project_id) project_id, type, git_username, encrypted_value
    FROM project_secrets
    WHERE use_for_git AND type IN ('ssh_key', 'password')
    ORDER BY project_id, updated_at DESC
) g
WHERE p.id = g.project_id;

DELETE FROM project_secrets
WHERE type = 'registry'
   OR (use_for_git AND env_var_name = '' AND NOT use_for_build);

ALTER TABLE project_secrets RENAME TO project_env_vars;

ALTER TABLE project_env_vars
    ADD COLUMN key       TEXT    NOT NULL DEFAULT '',
    ADD COLUMN sensitive BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN runtime   BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN build     BOOLEAN NOT NULL DEFAULT FALSE;

-- KEY: the env var name if one was set, else the build secret id, else the
-- display name normalised into a valid identifier.
UPDATE project_env_vars
SET key = COALESCE(
        NULLIF(env_var_name, ''),
        NULLIF(build_secret_id, ''),
        regexp_replace(upper(regexp_replace(name, '[^A-Za-z0-9_]', '_', 'g')), '^([0-9])', '_\1')
    ),
    sensitive = (type <> 'env_var'),
    runtime   = (env_var_name <> ''),
    build     = use_for_build;

-- Make keys unique per project (no duplicates exist in production at the time
-- of writing, but a later row with a clashing key gets a numeric suffix rather
-- than failing the migration).
UPDATE project_env_vars v
SET key = v.key || '_' || d.rn
FROM (
    SELECT id, row_number() OVER (PARTITION BY project_id, key ORDER BY created_at, id) - 1 AS rn
    FROM project_env_vars
) d
WHERE v.id = d.id AND d.rn > 0;

ALTER TABLE project_env_vars
    DROP COLUMN name,
    DROP COLUMN type,
    DROP COLUMN env_var_name,
    DROP COLUMN use_for_git,
    DROP COLUMN use_for_build,
    DROP COLUMN build_secret_id,
    DROP COLUMN git_username;

ALTER TABLE project_env_vars ALTER COLUMN key DROP DEFAULT;
ALTER TABLE project_env_vars ADD CONSTRAINT project_env_vars_key_check
    CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]*$');
CREATE UNIQUE INDEX project_env_vars_project_key ON project_env_vars (project_id, key);
