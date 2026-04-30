-- Allow users to customise their display name and avatar without OAuth login
-- overwriting the change. The two boolean flags are flipped to TRUE when the
-- user edits the field via PATCH /api/me; UpsertUser then preserves the
-- customised value on subsequent OAuth logins.

ALTER TABLE users ADD COLUMN IF NOT EXISTS name_overridden   BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_overridden BOOLEAN NOT NULL DEFAULT FALSE;
