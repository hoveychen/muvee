---
id: project-mesh
title: Project-to-Project Access (muvee-mesh)
sidebar_position: 8
---

# Project-to-Project Access (muvee-mesh)

Projects that belong to the **same owner** can call each other from inside
their containers over plain HTTP, without any credentials:

```bash
# inside a container of project "frontend"
curl http://backend.muvee.internal/api/items
```

`backend` is the target project's **domain prefix** (the `<prefix>` in
`<prefix>.<BASE_DOMAIN>`). The call skips the target's ForwardAuth login
entirely; instead, muvee tells the target *which project* is calling.

No code or configuration is needed in the calling project — the hostname just
resolves.

## What the target receives

Every request forwarded through the mesh carries:

| Header | Value |
|---|---|
| `X-Muvee-Caller-Project` | Name of the calling project |
| `X-Muvee-Caller-Project-Id` | UUID of the calling project |

The `Host` header stays `<prefix>.muvee.internal`. Any inbound `X-Muvee-Caller-*`
or `X-Forwarded-User*` header sent by the caller is **removed** before
forwarding, so a caller cannot impersonate another project or a signed-in user.
Mesh requests never carry `X-Forwarded-User` — use the caller headers to
authorise project-to-project calls.

```python
# Python / Flask
caller = request.headers.get("X-Muvee-Caller-Project")
if caller is not None:
    ...  # called by another project of the same owner
```

WebSocket and streaming (SSE / chunked) responses are passed through.

## Turning it on and off

The switch lives on the **target** project: "may other projects of mine call
this one". It is **on by default**, including for projects that existed before
the feature shipped.

- Web UI: project → **Config** → **Project-to-project access** → *Allow access from other projects*.
- CLI:

  ```bash
  muveectl projects update PROJECT_ID --allow-project-access=false   # refuse
  muveectl projects update PROJECT_ID --allow-project-access=true    # allow again
  ```

Changes take effect within about 5 seconds (the gateway caches decisions
briefly).

## Responses from the gateway

When a call is not forwarded, the gateway answers itself with a short
`muvee mesh: <reason>` body:

| Status | Reason | Meaning |
|---|---|---|
| 403 | `different_owner` | Target belongs to another owner |
| 403 | `access_disabled` | Target turned *Allow access from other projects* off |
| 403 | — | The source container is not a muvee project container on the mesh |
| 404 | `target_not_found` | No project with that domain prefix |
| 404 | `caller_not_found` | The calling container's project no longer exists |
| 503 | `target_not_running` | Target has no running deployment, or is paused |
| 502 | — | Control plane unreachable, or the target's port refused the connection |

A project may call itself through the mesh.

## Scope and limits

- **HTTP on port 80 only.** `http://<prefix>.muvee.internal/` — no HTTPS, no
  other ports. TCP-routed projects (`--tcp-domain`) are not reachable through
  the mesh.
- **Which containers can call.** Single-container projects (Dockerfile and
  image projects) join the mesh automatically. For **compose** projects only
  the **expose service** joins; the other services keep their private compose
  network and cannot resolve `*.muvee.internal`. A service that sets
  `network_mode` is never joined.
- **Existing containers must be redeployed** to become callers: the mesh
  network and DNS are attached when the container is created. Targets need no
  redeploy — the gateway reaches them through the same `HOST_IP:port` that
  Traefik uses.
- **Deploy agent must run as a docker container.** The gateway lives inside the
  deploy agent and is enabled only when the agent detects it is running in a
  container (the standard `docker-compose.yml` / `docker-compose.agent-deploy.yml`
  setups). A natively-run agent logs
  `muvee-mesh gateway disabled` and deploys without the mesh.

## How it works

```
 project A container ──DNS: backend.muvee.internal?──▶ gateway 10.213.0.2 (deploy agent)
        │                                                 │ answers 10.213.0.2
        └──HTTP Host: backend.muvee.internal ────────────▶│
                                                          │ caller = A (by source IP)
                                                          │ GET /api/agent/mesh/resolve?caller=A&target=backend
                                                          │   same owner? allowed? running?
                                                          └──▶ HOST_IP:port of backend (+ caller headers)
```

1. Each deploy node has a docker network `muvee-mesh` (default subnet
   `10.213.0.0/16`). The deploy agent attaches itself at a fixed IP
   (default `10.213.0.2`) and runs a DNS server and an HTTP proxy there.
2. Project containers join `muvee-mesh` and use the gateway as their first DNS
   server. `*.muvee.internal` resolves to the gateway; everything else is
   forwarded to the agent's own upstream resolvers.
3. The gateway identifies the caller by its source IP on `muvee-mesh` (looked up
   from the container's `muvee.domain_prefix` label), asks the control plane
   whether the call is allowed, and proxies it to the target's running
   deployment.

### Agent settings

All optional — the defaults work out of the box.

| Variable | Default | Description |
|---|---|---|
| `MESH_SUBNET` | `10.213.0.0/16` | Subnet used when the agent creates `muvee-mesh`. An existing network is reused with its own subnet. Change it only if it collides with your LAN/VPN. |
| `MESH_GATEWAY_IP` | `10.213.0.2` | Fixed IP of the agent on `muvee-mesh`; must lie inside the subnet. |
| `MESH_FALLBACK_DNS` | auto-detect | Comma-separated resolvers handed to project containers as a second DNS server, so external lookups keep working while the gateway is down. Auto-detected from what docker gives a container on the default bridge. |
| `MESH_SELF_CONTAINER` | auto-detect | The agent's own container name/ID, if auto-detection from `/proc/self/mountinfo` fails. |

### While the gateway is down

During an agent restart or upgrade the gateway is briefly unavailable:

- `*.muvee.internal` calls fail until the agent is back (it re-attaches itself
  and recovers automatically).
- **External DNS lookups in project containers become slow, not broken:** the
  first DNS server (the gateway) times out and the fallback answers. Measured
  on Docker 29, each lookup took about **8 seconds** while the agent was
  stopped, and returned to normal as soon as it came back.

## Security notes

The mesh is a convenience boundary inside a node you trust, not a hard
isolation boundary.

- **Cross-owner direct connections are possible (known v1 limitation).** All
  callers share the `muvee-mesh` network, so a container can bypass the gateway
  and connect to another project's container **by its mesh IP and container
  port**, regardless of owner. The gateway's owner and *allow access* checks
  only apply to calls made through `<prefix>.muvee.internal`.
- **Published ports are reachable from any project container.** This predates
  the mesh: any project container (on the mesh or not) can connect to
  `HOST_IP:<host port>` of any project on the node, which skips Traefik and
  therefore ForwardAuth — and such a direct request can carry any
  `X-Forwarded-User` or `X-Muvee-Caller-*` header it likes. ForwardAuth and
  these headers protect against the internet, not against other workloads on
  the same node.

If a project must not be reachable by other workloads on the node, run it on a
node shared only with projects you trust.
