-- Add build-time secret binding support.
-- use_for_build=true marks a secret to be injected to docker buildx via --secret.
-- build_secret_id maps to /run/secrets/<build_secret_id> inside Dockerfile RUN steps.
ALTER TABLE project_secrets
    ADD COLUMN IF NOT EXISTS use_for_build BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE project_secrets
    ADD COLUMN IF NOT EXISTS build_secret_id TEXT NOT NULL DEFAULT '';
