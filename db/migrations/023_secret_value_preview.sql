-- 023_secret_value_preview.sql
-- Add value_preview column for non-sensitive display and extend secret types to include api_key + env_var.
-- - password / ssh_key: value_preview remains empty (values are write-only).
-- - api_key: value_preview stores a masked fingerprint (head4 + **** + tail4).
-- - env_var: value_preview stores the full plaintext value (meant for non-sensitive env vars).

ALTER TABLE secrets ADD COLUMN value_preview TEXT NOT NULL DEFAULT '';

ALTER TABLE secrets DROP CONSTRAINT secrets_type_check;
ALTER TABLE secrets ADD CONSTRAINT secrets_type_check
    CHECK (type IN ('password', 'ssh_key', 'api_key', 'env_var'));
