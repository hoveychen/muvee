-- 003_http_provider.sql
-- Switch to Traefik HTTP provider: store container endpoint info on nodes and deployments.

ALTER TABLE nodes ADD COLUMN IF NOT EXISTS host_ip TEXT NOT NULL DEFAULT '';

ALTER TABLE projects ADD COLUMN IF NOT EXISTS container_port INT NOT NULL DEFAULT 8080;

ALTER TABLE deployments ADD COLUMN IF NOT EXISTS host_port INT NOT NULL DEFAULT 0;
