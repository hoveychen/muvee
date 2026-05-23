-- Per-project custom domain aliases. A project is reachable at
-- `<domain_prefix>.<base_domain>` by default; this table lets owners attach
-- additional hosts (e.g. `app.example.com`, `example.com`) that route to the
-- same backend.
--
-- Rules enforced by application code (see store layer):
--   - `host` is stored lowercased (CHECK below enforces it).
--   - Aliases must not collide with `<any prefix>.<base_domain>` — that namespace
--     is owned by the deployment / domain-only routers.
--   - Aliases must not equal `<base_domain>` itself — apex of the platform.
--   - Host shape is validated against RFC1123 dotted hostname (a..z, 0..9, '-',
--     '.', no leading/trailing dot or hyphen per label).
--
-- DNS prerequisites (out of band):
--   - Subdomain alias (e.g. `app.example.com`): CNAME → `<prefix>.<base_domain>`
--     (or A → server IP).
--   - Apex alias (e.g. `example.com`): A / ALIAS → server IP. CNAME at apex is
--     forbidden by DNS spec.
--   - Let's Encrypt HTTP-01 challenge runs on the first request after DNS
--     resolves; Traefik will keep serving the default self-signed cert until
--     the challenge completes (a few seconds).

CREATE TABLE IF NOT EXISTS project_aliases (
    id         UUID        PRIMARY KEY,
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    host       TEXT        NOT NULL UNIQUE CHECK (host = lower(host)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_project_aliases_project ON project_aliases(project_id);
