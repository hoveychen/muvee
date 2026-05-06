-- Image project type: deploy a single pre-built container image (Docker Hub,
-- GHCR, or any OCI registry) without a git repo. The deploy path reuses the
-- compose runtime — the scheduler synthesises a tiny inline compose YAML from
-- image_ref + container_port and the agent runs it the same way as a regular
-- compose project. Auto-deploy uses the existing image-digest watcher.

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_project_type_check;
ALTER TABLE projects ADD CONSTRAINT projects_project_type_check
    CHECK (project_type IN ('deployment', 'domain_only', 'compose', 'image'));

ALTER TABLE projects ADD COLUMN IF NOT EXISTS image_ref TEXT NOT NULL DEFAULT '';
