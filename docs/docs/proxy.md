---
id: proxy
title: Proxy Configuration
sidebar_position: 11
---

# Proxy Configuration

If your muvee deployment runs in a network that requires an HTTP proxy to reach the public internet, you can configure it via a `.proxy.env` file. The proxy settings are loaded automatically by Docker Compose — no code changes are needed.

## Which services are affected

| Service | Why it needs proxy access |
|---------|--------------------------|
| `muvee-authservice` | Calls external OAuth provider APIs: Google, Feishu/Lark, WeCom, DingTalk, Discord, Facebook, Apple, Twitter |
| `muvee-agent-builder` | Clones project source code via `git clone` over HTTPS; forwards proxy into docker builds so `RUN` commands (pip, apt-get, curl, npm…) inside Dockerfiles use the same proxy |
| `muvee-agent-deploy` | Clones the project's compose repository via `git clone` over HTTPS to fetch the latest compose files before each deployment |

:::note What is not affected
`docker pull` and `docker push` go through the host Docker daemon socket — configure the host `dockerd` proxy separately if needed.

SSH-based git clones are not affected by HTTP proxy settings. Use HTTPS + token authentication (via the muvee Secrets mechanism) instead.

User containers started by the deploy agent do **not** inherit the agent's proxy settings by default. See [Deploy-time proxy isolation](#deploy-time-proxy-isolation) below.
:::

## Setup

`.proxy.env` is already present in the repository (empty by default). No extra setup step is needed — docker compose loads it automatically.

**No proxy needed:** leave `.proxy.env` as-is (empty).

**Behind a proxy:** edit `.proxy.env` directly. After adding credentials, `git status` will show the file as `modified` — this is expected. Do not stage or commit those changes (see [File security](#file-security) below).

```env title=".proxy.env"
HTTPS_PROXY=http://<proxy-host>:<port>
HTTP_PROXY=http://<proxy-host>:<port>
https_proxy=http://<proxy-host>:<port>
http_proxy=http://<proxy-host>:<port>
NO_PROXY=localhost,127.0.0.1,127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1,muvee-server,muvee-authservice,registry,postgres,traefik,.local,.internal,.lan,.localdomain
no_proxy=localhost,127.0.0.1,127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,::1,muvee-server,muvee-authservice,registry,postgres,traefik,.local,.internal,.lan,.localdomain
```

Both uppercase (`HTTP_PROXY`) and lowercase (`http_proxy`) forms are provided because different tools have different conventions: Go's `net/http` reads both, `git` (libcurl) reads only lowercase.

## Build-time proxy passthrough

When a proxy is configured, muvee automatically forwards `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` into every `docker buildx build` invocation as `--build-arg` values. This means `RUN` commands inside your Dockerfile — `pip install`, `apt-get`, `curl`, `npm install`, etc. — use the proxy without any Dockerfile changes required.

Each deployment log shows a `[proxy]` line confirming the proxy state before the build starts:

```
[proxy] forwarding into build: HTTP_PROXY, HTTPS_PROXY, NO_PROXY, http_proxy, https_proxy, no_proxy
```

or, when no proxy is configured:

```
[proxy] passthrough enabled but no proxy vars are set; build will use direct network access
```

### Disabling build-time passthrough

If you want the build environment isolated from the proxy (for example, the proxy is only needed for `git clone` or OAuth calls, not for package downloads), set in `.proxy.env`:

```env
BUILDER_PROXY_PASSTHROUGH=false
```

Accepted false values: `false`, `0`, `no`, `off` (case-insensitive). Any other value, or leaving the variable unset, keeps passthrough enabled.

## Deploy-time proxy isolation

When deploying a compose project, `muvee-agent-deploy` runs `docker compose pull` and `docker compose up`. Docker Compose v2 forwards proxy variables from the calling process into containers when the user's compose file explicitly requests inheritance — via value-less `environment` entries or YAML variable interpolation:

```yaml title="common patterns that inherit proxy from the host"
services:
  myapp:
    environment:
      - HTTP_PROXY          # value-less: inherits from calling process
      - HTTPS_PROXY
    # or YAML interpolation:
    # environment:
    #   HTTP_PROXY: ${HTTP_PROXY}
```

When the deploy agent has `HTTP_PROXY` set and a user compose file uses either pattern, the agent's proxy leaks into user containers. This can break intra-service calls to internal Docker-network hostnames like `hub-server` or `redis` that must not go through an external proxy.

**By default, muvee strips all proxy variables before calling `docker compose`**, so user containers start with a clean proxy-free environment regardless of what their compose files declare. The agent's proxy is used only for the preceding `git clone` step.

If you use `HTTP_PROXY: ${HTTP_PROXY}` or `environment: - HTTP_PROXY` in your compose file expecting the deploy-node proxy to be available, it will resolve to an empty string. To give your containers proxy access, set it explicitly in your compose file or via a muvee project secret:

```yaml title="docker-compose.yml (user project)"
services:
  myapp:
    image: myimage
    environment:
      HTTP_PROXY: "http://my-proxy:3128"
      NO_PROXY: "localhost,127.0.0.1,10.0.0.0/8"
```

### Enabling passthrough (opt-in)

If your user containers genuinely need to inherit the deploy-node proxy — for example, every deployed project on this node should use the same corporate proxy — set in `.proxy.env`:

```env
DEPLOYER_PROXY_PASSTHROUGH=true
```

Accepted truthy values: `true`, `1`, `yes`, `on` (case-insensitive). Any other value, or leaving it unset, keeps isolation enabled (default).

:::warning Passthrough requires a complete NO_PROXY list
When passthrough is enabled, ensure `NO_PROXY` covers all Docker-internal service names used by your compose projects (e.g. `hub-server`, `redis`, `minio`). Omitting them will cause those services to route through the external proxy and fail with connection timeouts.
:::

Only the standard proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `ALL_PROXY`, `FTP_PROXY` and their lowercase forms) are forwarded. Agent-private variables (`AGENT_SECRET`, database credentials, tokens, etc.) are never exposed to user containers regardless of this setting.

## NO_PROXY — internal services

The `NO_PROXY` list ensures that traffic between muvee's own services is never routed through the proxy:

| Entry | Why |
|-------|-----|
| `muvee-server` | Agent polls control-plane API; authservice calls `/api/internal/*` endpoints |
| `muvee-authservice` | Server calls `/_oauth/internal/reload` on authservice |
| `registry` | Docker Compose service name of the private registry container |
| `postgres` | Database — server-internal, never leaves the compose network |
| `traefik` | Reverse proxy — server-internal |
| `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` | All standard LAN CIDR ranges |
| `.local`, `.internal`, `.lan`, `.localdomain` | Local / split-horizon DNS suffixes |

## File security

`.proxy.env` is tracked in git as an empty file, so it is present immediately after `git clone` and is safe to share in that state. Once you add real proxy credentials, your local edits will appear as `modified` in `git status` — **do not stage or commit those changes**.

To prevent accidental commits on machines with real credentials configured:

```bash
git update-index --skip-worktree .proxy.env
```

The annotated reference template is in `.proxy.env.example`.

## Multi-node deployments

For multi-node setups (`docker-compose.agent-builder.yml` / `docker-compose.agent-deploy.yml`), the same `.proxy.env` approach applies:

- **If you `git clone` the repo on each agent node**: the empty `.proxy.env` is already there — edit it to configure proxy settings if needed.
- **If you ship only the compose file to the node** (e.g., via `scp`): create an empty `.proxy.env` alongside it first — it is required by `env_file:` even when no proxy is used:

  ```bash
  touch .proxy.env
  ```
