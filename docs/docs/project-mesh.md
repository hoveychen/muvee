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

All optional — the defaults work out of the box for a single muvee stack per
docker host.

| Variable | Default | Description |
|---|---|---|
| `MESH_NETWORK` | `muvee-mesh` | Name of the mesh docker network. Required when several muvee stacks share one docker host (see below). |
| `MESH_SUBNET` | `10.213.0.0/16` | Subnet used when the agent creates `muvee-mesh`. An existing network is reused with its own subnet. Change it only if it collides with your LAN/VPN. |
| `MESH_GATEWAY_IP` | `10.213.0.2` | Fixed IP of the agent on `muvee-mesh`; must lie inside the subnet. |
| `MESH_FALLBACK_DNS` | auto-detect | Comma-separated resolvers handed to project containers as a second DNS server, so external lookups keep working while the gateway is down. Auto-detected from what docker gives a container on the default bridge. |
| `MESH_SELF_CONTAINER` | auto-detect | The agent's own container name/ID, if auto-detection from `/proc/self/mountinfo` fails. |

**Several muvee stacks on one docker host** (e.g. two control planes whose
deploy agents run on the same machine): docker networks are host-global, so
each stack's deploy agent **must** set its own `MESH_NETWORK` and a
non-overlapping `MESH_SUBNET` (e.g. `MESH_NETWORK=insnap-mesh`,
`MESH_SUBNET=10.214.0.0/16`). With the defaults both agents compete for
`10.213.0.2` on the same `muvee-mesh`.

- **Copying a stack's `.env` / compose directory to start a new stack:** change
  both `MESH_NETWORK` and `MESH_SUBNET`. Changing only the subnet does nothing:
  an existing network of that name is reused with its own subnet.
- **`MESH_NETWORK` must name a network of its own** that the agent creates and
  labels `muvee.mesh=1`. The agent refuses any existing network without that
  label (e.g. the stack's compose network): it logs
  `mesh: docker network ... is not a muvee mesh network`, keeps the mesh
  disabled and never touches its own attachment to that network.
- **Gateway IP already taken:** before attaching, the agent checks the gateway
  IP. When another container holds it — running at that IP, or stopped /
  created and still reserving it as a fixed IP — the agent logs
  `mesh: gateway IP ... is already held by container ...`, keeps the mesh
  disabled and retries every 30s; the agent itself keeps working and its
  container restarts normally. A *running* container that only carries a
  leftover entry for the IP (no live address) does not count.
- **Held by another stack:** when the holder belongs to a different compose
  project (or, without compose, is a differently named container), the agent
  logs `mesh: WARNING: gateway IP ... is held by ... of another muvee stack
  (compose project ...)` and **remembers it** in
  `$DATA_DIR/mesh-foreign-gateway-holders.json` (the agent's data volume, kept
  across upgrades). From then on it never takes the gateway IP on that network,
  even when it looks free — otherwise it would grab it while Watchtower
  recreates the other stack's agent (old container removed, new one not yet
  started), and the other, healthy stack's agent would be stuck. The record is
  per network: it disappears when that network is recreated. To retry
  anyway, delete the file and restart the agent, or change `MESH_NETWORK`.
- **The stack that already has the gateway can still lose it** while the
  stacks share a network: if the other agent does take the IP during an
  upgrade gap (e.g. it runs an older muvee, or its record was deleted), the
  upgraded agent's new container stays in `Created` and fails to start with
  `Address already in use`. Recover with `docker rm <stuck container>` and
  recreate the agent (`docker compose up -d <agent service>`), then give the
  stacks separate networks.
- **After changing `MESH_NETWORK`, redeploy every project of that stack.**
  The agent creates the new network, but projects deployed earlier stay on the
  old one, with their DNS still pointing at the gateway IP there — which may
  now be the *other* stack's agent. That gateway identifies callers only by
  their `muvee.domain_prefix` label, so a leftover project would be treated as
  a project of the other stack with the same prefix.

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
