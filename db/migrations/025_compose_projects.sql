-- Compose project type: muvee deploys a docker-compose project (image: only)
-- onto a single pinned deploy node. Persistence is whatever the compose file
-- declares (docker named volumes / local bind-mounts), and the project is
-- bound to one node so those volumes survive across redeploys.

ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_project_type_check;
ALTER TABLE projects ADD CONSTRAINT projects_project_type_check
    CHECK (project_type IN ('deployment', 'domain_only', 'compose'));

ALTER TABLE projects ADD COLUMN IF NOT EXISTS compose_file_path TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS expose_service    TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS expose_port       INT  NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS pinned_node_id    UUID NULL REFERENCES nodes(id) ON DELETE SET NULL;
