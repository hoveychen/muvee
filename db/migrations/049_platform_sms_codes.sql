-- Platform (admin-plane) phone login reuses the sms_verification_codes table,
-- but the platform login has no project. Relax project_id to nullable so a
-- platform code is stored with project_id IS NULL; downstream project codes
-- keep a non-null project_id. The FK to projects stays: a NULL value simply
-- skips the foreign-key check, so this keeps referential integrity for
-- project-scoped codes while allowing project-less platform codes.
ALTER TABLE sms_verification_codes
    ALTER COLUMN project_id DROP NOT NULL;
