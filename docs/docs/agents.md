---
id: agents
title: Agent Nodes
sidebar_position: 8
---

# Agent Nodes

A single `muvee-agent` binary handles both builder and deploy roles. The role is determined by the `NODE_ROLE` environment variable.

## Security: Agent Secret

All agent ↔ control plane communication is protected by a **shared secret** (`AGENT_SECRET`). The agent includes this value in every request as an `X-Agent-Secret` HTTP header. The server rejects requests with an incorrect or missing header with `401 Unauthorized`.

Set the same `AGENT_SECRET` value on the control plane and all agent nodes. Generate a strong secret with:

```bash
openssl rand -hex 32
```

If `AGENT_SECRET` is not set, the server logs a warning and accepts all agent requests (useful for local development only).

## Communication Protocol

Agents use a **long-poll pull model** — no inbound connections from the control plane are needed.

```
Agent → GET /api/agent/tasks?node_id={id}        (every 5s)
     header: X-Agent-Secret: <secret>
     ← []Task (pending tasks for this node)

Agent → POST /api/agent/tasks/{id}/complete
     header: X-Agent-Secret: <secret>
     body: { status, result, image_tag? }          # build tasks
     body: { status, host_port }                   # deploy tasks
```

Agents only need outbound connectivity to `CONTROL_PLANE_URL` — they can sit behind NAT or firewalls.

:::caution CONTROL_PLANE_URL must be an internal address
Set `CONTROL_PLANE_URL` to the **internal network address** of the control plane (e.g. `http://10.0.0.1:8080`), not the public-facing domain.

Two reasons:
1. The agent auto-detects its `HOST_IP` by observing which network interface is used to reach the control plane. Using the internal address ensures the correct interface (and IP) is selected, so Traefik can route traffic back to the container.
2. There is no need to go through the public internet — agent endpoints are not protected by OAuth.
:::

## Heartbeat

Agents send a registration request on startup (and periodically on reconnect). The registration payload includes the node's `host_ip` — the IP Traefik will use to reach containers deployed on this node. This is auto-detected by finding the local interface used to reach the control plane.

The control plane marks a node as offline if `last_seen_at` is older than 2 minutes. Offline nodes are excluded from scheduling.

## Registry Authentication

Both builder and deploy nodes must authenticate with the private registry:

- **Builder** — pushes the newly built image (`docker buildx build --push`)
- **Deploy** — pulls the image before starting the container (`docker run` triggers an implicit `docker pull` if the image is not cached locally)

Set `REGISTRY_USER` and `REGISTRY_PASSWORD` on every agent node. The agent runs `docker login <REGISTRY_ADDR>` automatically on startup.

### REGISTRY_ADDR: public vs. internal address

`REGISTRY_ADDR` is only used by agent nodes (for `docker login`, image push, and image pull). The control plane never contacts the registry directly, so there is no requirement to expose the registry on a public domain.

If all agent nodes are on the same internal network as the registry, you can point `REGISTRY_ADDR` at the internal address instead of the Traefik-proxied public domain:

| Setup | Example REGISTRY_ADDR |
|---|---|
| Public domain via Traefik (default) | `registry.example.com` |
| Same Docker network as registry container | `registry:5000` |
| Same LAN / VPC | `10.0.0.1:5000` |

:::caution Plain HTTP registries require Docker daemon configuration
The built-in registry container (`registry:2`) listens on plain **HTTP** on port 5000. Traefik adds TLS at the edge, so the public domain works out of the box. When using an internal address that bypasses Traefik, the connection is unencrypted HTTP, and Docker will refuse to push or pull by default.

Add the internal address to `insecure-registries` on **every agent node**:

```json title="/etc/docker/daemon.json"
{
  "insecure-registries": ["10.0.0.1:5000"]
}
```

Then restart Docker:

```bash
sudo systemctl restart docker
```
:::

Using an internal address is generally preferred for co-located nodes — it avoids the public internet round-trip and removes the dependency on DNS / Let's Encrypt.

## Builder Node

Requires:
- `git` CLI
- `docker` CLI with `buildx` support
- `REGISTRY_USER` / `REGISTRY_PASSWORD` set

On receiving a build task:

1. `git clone --depth=1 --branch {branch} {git_url}` into a temp directory
2. `docker buildx build -f {dockerfile} -t {registry}/{project}:{sha} --push`
3. Report completion with `image_tag`

## Deploy Node

Requires:
- `docker` CLI
- `rsync` (for dependency datasets)
- NFS mounted at the same path as configured in each Dataset's `nfs_path`
- `REGISTRY_USER` / `REGISTRY_PASSWORD` set (to pull images)
- Network connectivity back to the control plane (for `CONTROL_PLANE_URL`)

On receiving a deploy task:

1. For each `dependency` dataset: rsync from NFS, symlink into deployment mount dir
2. For each `readwrite` dataset: prepare direct NFS bind-mount path
3. `docker rm -f muvee-{domain_prefix}` (rolling update: stop old container)
4. `docker run -d --name muvee-{domain_prefix} -p 0:{container_port} ... {image_tag}` — Docker assigns a random host port
5. `docker port muvee-{domain_prefix} {container_port}` — discover the assigned host port
6. Report completion with `host_port`; the control plane updates the Traefik HTTP provider config
7. Traefik picks up the new route within 5 seconds
