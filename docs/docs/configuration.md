---
id: configuration
title: Configuration Reference
sidebar_position: 3
---

# Configuration Reference

All configuration is via environment variables.

## Control Plane (`muvee-server`)

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://muvee:muvee@localhost:5432/muvee?sslmode=disable` | PostgreSQL connection string |
| `MIGRATIONS_DIR` | `./db/migrations` | Path to SQL migration files |
| `PORT` | `8080` | HTTP listen port |
| `BASE_DOMAIN` | `localhost` | Root domain; projects are served at `{prefix}.BASE_DOMAIN`. Also distributed to agents via `/api/agent/config`. |
| `BASE_DOMAINS` | — | Optional comma-separated list of **additional** base domains to serve the same instance under (multi-domain). `BASE_DOMAIN` stays the canonical default. See [Multi-domain](#multi-domain) below. |
| `GOOGLE_CLIENT_ID` | — | Google OAuth2 client ID. If set, enables Google login. See [Google OAuth2](./auth/auth-google). |
| `GOOGLE_CLIENT_SECRET` | — | Google OAuth2 client secret |
| `GOOGLE_REDIRECT_URL` | `http://localhost:8080/auth/google/callback` | Google OAuth2 callback URL |
| `FEISHU_APP_ID` | — | Feishu / Lark App ID. If set, enables Feishu login. See [Feishu / Lark](./auth/auth-feishu). |
| `FEISHU_APP_SECRET` | — | Feishu / Lark App Secret |
| `FEISHU_REDIRECT_URL` | `http://localhost:8080/auth/feishu/callback` | Feishu OAuth2 callback URL |
| `FEISHU_BASE_URL` | `https://open.feishu.cn` | Feishu API base URL. Set to `https://open.larksuite.com` for international Lark. |
| `WECOM_CORP_ID` | — | WeCom (企业微信) Corp ID. If set, enables WeCom login. See [WeCom](./auth/auth-wecom). |
| `WECOM_CORP_SECRET` | — | WeCom App Secret |
| `WECOM_AGENT_ID` | — | WeCom Agent ID of the internal app |
| `WECOM_REDIRECT_URL` | `http://localhost:8080/auth/wecom/callback` | WeCom OAuth2 callback URL |
| `DINGTALK_CLIENT_ID` | — | DingTalk (钉钉) App Key. If set, enables DingTalk login. See [DingTalk](./auth/auth-dingtalk). |
| `DINGTALK_CLIENT_SECRET` | — | DingTalk App Secret |
| `DINGTALK_REDIRECT_URL` | `http://localhost:8080/auth/dingtalk/callback` | DingTalk OAuth2 callback URL |
| `ALLOWED_DOMAINS` | _(allow all)_ | Comma-separated email domains allowed to sign in (e.g. `company.com`). Applied to Google; enterprise SSO providers (Feishu, WeCom, DingTalk) bypass this check when no real email is available and a synthetic `*.local` address is used instead. |
| `ADMIN_EMAILS` | — | Comma-separated email addresses that are auto-promoted to `admin` on login and can access `traefik.BASE_DOMAIN` |
| `JWT_SECRET` | `change-me-in-production` | Secret for signing JWT session tokens |
| `AGENT_SECRET` | — | Shared secret for agent ↔ server authentication (set the same value on all agents). If unset, agent endpoints are unauthenticated (dev only). |
| `AUTH_SERVICE_URL` | `http://muvee-authservice:4181` | Internal URL of `muvee-authservice`; used when generating per-project ForwardAuth config for Traefik |
| `REGISTRY_ADDR` | `localhost:5000` | Docker registry address. Distributed to agents via `/api/agent/config` — agents do not need this set locally. |
| `REGISTRY_USER` | — | Registry Basic Auth username. Distributed to agents — they run `docker login` automatically on startup. |
| `REGISTRY_PASSWORD` | — | Registry Basic Auth password. Distributed to agents. |
| `SECRET_ENCRYPTION_KEY` | — | 64-character hex string (32 bytes) used to encrypt secrets at rest with AES-256-GCM. Required to enable the Secrets feature. Generate with `openssl rand -hex 32`. |
| `VOLUME_NFS_BASE_PATH` | — | Base NFS directory on the control plane host used for project workspace volumes (e.g. `/mnt/nfs/volumes`). A per-project subdirectory is created automatically under this path. Also distributed to deploy agents via `/api/agent/config` so they can bind-mount the volume into containers. If unset, the workspace feature is disabled. |
| `DATASET_NFS_BASE_PATH` | — | Base NFS directory for datasets (e.g. `/mnt/nfs/datasets`). Dataset `nfs_path` is treated as a relative sub-path under this base (e.g. `warehouse` → `/mnt/nfs/datasets/warehouse`). Mount at the same absolute path on all relevant nodes. The **control-plane server needs write access (`:rw`)** — it runs the file monitor and serves dataset file operations (upload/delete/mkdir/move/copy), so the NFS export must allow the server host to write. Deploy agents only need read access (`:ro`, for rsync and bind-mount into user containers). |
| `GIT_REPO_BASE_PATH` | — | Directory where bare git repositories are stored for hosted projects (e.g. `/data/git`). Each hosted project gets a `{project_id}.git` subdirectory. If unset, the hosted repository feature is disabled and all projects must use an external git URL. |
| `TUNNEL_BACKEND_URL` | — | Internal URL that Traefik uses to route adhoc tunnel traffic back to this server (e.g. `http://muvee-server:8080`). Required to enable `muveectl tunnel`. Set automatically in the default Docker Compose setup. |

## ForwardAuth Service (`muvee-authservice`)

| Variable | Default | Description |
|---|---|---|
| `PORT` | `4181` | HTTP listen port |
| `GOOGLE_CLIENT_ID` | — | Same as control plane |
| `GOOGLE_CLIENT_SECRET` | — | Same as control plane |
| `FORWARD_AUTH_REDIRECT_URL` | `http://localhost:4181/_oauth` | OAuth2 callback URL for ForwardAuth. In production set to `https://BASE_DOMAIN/_oauth` and register it in Google Cloud Console alongside `GOOGLE_REDIRECT_URL`. |
| `BASE_DOMAIN` | — | Root domain. Required so the session cookie is shared across all `*.BASE_DOMAIN` subdomains (e.g. `traefik.BASE_DOMAIN`, project subdomains). |
| `JWT_SECRET` | — | Must match the control plane value |
| `ADMIN_EMAILS` | — | Must match the control plane value; used to gate `/verify-admin` (Traefik dashboard) |

## Agent (`muvee-agent`)

| Variable | Default | Description |
|---|---|---|
| `NODE_ROLE` | _(required)_ | `builder` or `deploy` |
| `CONTROL_PLANE_URL` | `http://localhost:8080` | **Internal** address of the control plane (e.g. `http://10.0.0.1:8080`). Do not use the public domain — see [Agent Nodes](./agents) for details. |
| `AGENT_SECRET` | — | Must match the value set on the control plane |
| `DATA_DIR` | `/muvee/data` | Local dataset cache root (deploy nodes) |
| `HOST_IP` | _(auto-detect)_ | IP address Traefik uses to reach containers on this node. Auto-detected from the network interface used to reach `CONTROL_PLANE_URL`. Override if auto-detection selects the wrong interface (e.g. on multi-NIC hosts). |

:::info Registry credentials and BASE_DOMAIN are distributed automatically
Agents fetch `REGISTRY_ADDR`, `REGISTRY_USER`, `REGISTRY_PASSWORD`, and `BASE_DOMAIN` from the control plane via `GET /api/agent/config` on startup. You only need to set these on the control plane — there is no need to configure them on individual agent nodes.
:::

## Multi-domain

By default muvee serves everything under a single `BASE_DOMAIN`. To host the
**same instance** under more than one apex — for example an overseas domain and
a mainland/ICP domain — set `BASE_DOMAINS` to a comma-separated list of the
additional base domains:

```bash
BASE_DOMAIN=muveeai.com          # canonical default
BASE_DOMAINS=muveeai.com,muvee.ai
```

`BASE_DOMAIN` remains the canonical fallback (used when a request host matches
none of the configured domains). Per request muvee resolves which base domain
the host belongs to, so all of the following follow the domain the user is
actually on:

- **Auth cookies** are scoped to the matching base domain (a login on
  `muvee.ai` yields a `.muvee.ai` cookie, shared across its subdomains).
- **OAuth redirect_uri** is rebased onto the matching base domain, kept
  canonical to a single host per domain (e.g. `https://app.muvee.ai/_oauth/google`)
  so project subdomains still bounce through the panel/authservice host.
- **Traefik routers** match every project/tunnel prefix under each base domain
  via a single OR'd `Host()` rule, each with its own TLS cert entry.
- **CORS / origin** checks accept the canonical origin of every configured base
  domain and any of their subdomains.

### Requirements per extra base domain

- **DNS:** point `<base>` (A record) and `*.<base>` (wildcard A record) at this
  server's IP, same as the canonical domain.
- **TLS:** there is **no wildcard cert** for the extra base domains (unless the domain is a [Cloudflare Tunnel domain](#cloudflare-tunnel-domains)) — every
  project subdomain obtains its own Let's Encrypt HTTP-01 certificate, so the
  domain must be publicly reachable on port 80 for the ACME challenge.
- **OAuth provider dashboards:** register each domain's callback URLs with every
  provider you use, e.g. `https://app.<base>/auth/google/callback` (control-panel
  login) and `https://app.<base>/_oauth/google` (per-project ForwardAuth).

### Cloudflare Tunnel domains

A base domain can instead be served through a
[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/),
alongside the ACME domains. Cloudflare holds the TLS certificate at its edge.
The `cloudflared` container dials out to Cloudflare, so this domain needs no
open ports and no Let's Encrypt. The other base domains keep working as before
(DNS → public `:80`/`:443` → HTTP-01).

```bash
BASE_DOMAINS=muveeai.com,example.net
CF_TUNNEL_DOMAINS=example.net      # must be one of BASE_DOMAINS
CF_TUNNEL_TOKEN=eyJhIjoi...        # the tunnel's connector token
COMPOSE_PROFILES=cftunnel          # e.g. auto-update,cftunnel with watchtower
```

1. In the Cloudflare dashboard, create a tunnel (Zero Trust → Networks →
   Tunnels) and copy its token into `CF_TUNNEL_TOKEN`.
2. Add two public hostnames to the tunnel, `example.net` and `*.example.net`,
   both with the service `http://localhost:8000`. Cloudflare creates the apex
   CNAME for you; add the wildcard `*` CNAME to `<tunnel-id>.cfargotunnel.com`
   yourself if the dashboard doesn't.
3. Set SSL/TLS → Edge Certificates → **Always Use HTTPS** on.
4. Pull the updated `traefik/traefik.yml` (it adds the internal `cftunnel`
   entrypoint) and run `docker compose up -d`.

How it works: `cloudflared` shares Traefik's network namespace and forwards to
Traefik's `cftunnel` entrypoint (`:8000`). That entrypoint is plain HTTP and
never published on the host. It trusts `X-Forwarded-*` only from loopback, so
apps see the real client IP and `X-Forwarded-Proto: https`. muvee-server serves
every host under a tunnel domain on that entrypoint, with no certificate
resolver. It also adds panel routers for `<base>` and `app.<base>`, plus a
`/_oauth` router, so no extra compose labels are needed. The admin
Certificates panel lists these hosts as **Cloudflare**.

Limits on tunnel domains:

- **TCP routes** are not available. They depend on SNI passthrough on
  `:443`, which the tunnel doesn't carry. They stay reachable on the ACME
  domains.
- **Uploads**: Cloudflare caps a request body at 100 MB on the Free and Pro
  plans. Use an ACME domain for large `muveectl dataset push` uploads.
  `registry.<base>` and `traefik.<base>` are not served on tunnel domains.
- **Nested subdomains** such as `a.b.example.net` aren't covered by
  Cloudflare's free Universal SSL certificate.
- Register the tunnel domain's OAuth callbacks with each provider, the same as
  for any extra base domain.

## Runtime settings (admin UI)

A few knobs are stored in the `system_settings` table and edited through
the admin UI rather than environment variables. They take effect without a
restart.

| Key | Default | Notes |
|---|---|---|
| `auto_deploy_master_enabled` | `true` | Global kill switch for both auto-deploy triggers. |
| `auto_deploy_poll_interval_seconds` | `60` | Git poll cadence for external repos (min `10`). |
| `auto_deploy_image_watch_interval_seconds` | `600` | Image-digest poll cadence for compose projects (min `60`). |
| `enabled_project_types` | *(empty)* | Comma-separated whitelist of project types users may create. Empty = no restriction. |

See [Auto Deploy](./auto-deploy) for the full behaviour and the per-project
toggle.

### Restricting project types

`enabled_project_types` narrows which project types can be created on this
platform — edited under **Admin → Settings → Project types users may create**.
Valid values are `deployment`, `compose`, `image`, `build` and `domain_only`;
leaving it empty allows all of them (including types added in later releases).

The typical use is a control plane with little memory: `deployment` and
`build` both run `docker build` on a builder node, so setting the whitelist to
`compose,image,domain_only` keeps the platform to project types that only pull
pre-built images.

The check applies on **creation only**, to everyone including admins. A
project's type is immutable after creation, so projects of a since-disabled
type keep deploying as before — narrowing the list stops new load from being
added, it does not break what is already running. An admin who needs a
disabled type re-enables it in the settings page first.
