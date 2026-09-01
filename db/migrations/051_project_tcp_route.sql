-- 051: Per-project TCP route — TLS terminated by Traefik, routed by SNI.
--
-- Why this exists: some workloads speak a protocol that is TLS-wrapped but not
-- HTTP, so they cannot ride the normal HTTP router. The motivating case is a
-- self-hosted LiveKit TURN server, which must be reachable on **443** (that is
-- the only port locked-down corporate networks allow) while Traefik already
-- owns 443 on the node. Traefik's TCP routers solve exactly this: they share
-- the `websecure` entrypoint, match on SNI, terminate TLS, and hand plaintext
-- to the backend (LiveKit calls this `turn.external_tls`).
--
-- Two columns, both NULLable; NULL/0 on either means "no TCP route":
--   tcp_domain_prefix — the hostname (prefix under every base domain) whose
--                       SNI is routed to the backend.
--   tcp_host_port     — the port on the deploy node to forward to.
--
-- **The port is NOT published by muvee.** The project publishes it itself in
-- its own compose file (`ports:` entries pass through the deploy override
-- untouched). muvee only routes. That split is deliberate: the workloads that
-- need a TCP route also tend to need raw UDP alongside it, which no router can
-- carry — so port publishing stays where it already works, and this feature
-- only adds the one thing compose cannot do, namely terminate TLS on 443.
--
-- The prefix must not collide with any project's domain_prefix: Traefik gives
-- TCP routers precedence over HTTP routers on a shared entrypoint, so a
-- colliding prefix would silently swallow that project's HTTPS traffic. The
-- unique index below covers tcp-vs-tcp; tcp-vs-http is checked in the API
-- layer (a cross-column exclusion can't be expressed as a plain unique index).

ALTER TABLE projects ADD COLUMN IF NOT EXISTS tcp_domain_prefix TEXT;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS tcp_host_port     INTEGER;

ALTER TABLE projects ADD CONSTRAINT projects_tcp_host_port_range_chk
    CHECK (tcp_host_port IS NULL OR (tcp_host_port BETWEEN 1 AND 65535));

-- Two projects cannot claim the same TCP hostname — the second one would be
-- unreachable (or worse, non-deterministically shadow the first).
CREATE UNIQUE INDEX IF NOT EXISTS projects_tcp_domain_prefix_uq
    ON projects (tcp_domain_prefix)
    WHERE tcp_domain_prefix IS NOT NULL AND tcp_domain_prefix <> '';
