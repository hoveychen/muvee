-- 032: Admin-only fixed-port + fixed-deploy-node binding for projects.
--
-- When both fixed_host_port and fixed_node_id are set, deployments for this
-- project are forced onto the chosen node and the container's exposed port is
-- published on that exact host port (instead of letting Docker pick a random
-- ephemeral port). Traefik subdomain routing is unaffected — fixed-port and
-- domain access coexist.
--
-- Both columns are NULLable: NULL on either means "no fixed binding" and the
-- project falls back to the regular dynamic node + ephemeral port allocator.
-- The CHECK constraint enforces both-or-neither so we never end up with a
-- half-set state.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS fixed_host_port INTEGER;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS fixed_node_id   UUID
    REFERENCES nodes(id) ON DELETE SET NULL;

-- Port range guard. The both-or-neither rule between fixed_host_port and
-- fixed_node_id is enforced at the API layer, not via CHECK, because nodes(id)
-- ON DELETE SET NULL can leave fixed_node_id=NULL while fixed_host_port still
-- holds a value; a CHECK constraint would block that legitimate cleanup path.
ALTER TABLE projects ADD CONSTRAINT projects_fixed_port_range_chk
    CHECK (fixed_host_port IS NULL OR (fixed_host_port BETWEEN 1024 AND 65535));

-- Two projects on the same node cannot claim the same fixed host port.
CREATE UNIQUE INDEX IF NOT EXISTS projects_fixed_node_port_uq
    ON projects (fixed_node_id, fixed_host_port)
    WHERE fixed_host_port IS NOT NULL AND fixed_node_id IS NOT NULL;
