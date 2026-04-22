-- 024_api_token_expires_at.sql
-- Add optional expiration to API tokens. NULL = no expiry.
-- User-level personal access tokens (project_id IS NULL) may set an expiry;
-- project tokens typically keep NULL.
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
