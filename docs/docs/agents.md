---
id: agents
title: Agent Nodes
sidebar_position: 8
---

# Agent Nodes

A single `muvee-agent` binary handles both builder and deploy roles. The role is determined by the `NODE_ROLE` environment variable.

## Communication Protocol

Agents use a **long-poll pull model** — no inbound connections from the control plane are needed.

```
Agent → GET /api/agent/tasks?node_id={id}   (every 5s)
     ← []Task (pending tasks for this node)

Agent → POST /api/agent/tasks/{id}/complete
     body: { status, result, image_tag? }
```

This means agents can sit behind NAT or firewalls — only outbound HTTPS to the control plane is required.

## Heartbeat

Agents send a registration request on startup (and periodically on reconnect). The control plane marks a node as offline if `last_seen_at` is older than 2 minutes. Offline nodes are excluded from scheduling.

## Builder Node

Requires:
- `git` CLI
- `docker` CLI with `buildx` support

On receiving a build task:

1. `git clone --depth=1 --branch {branch} {git_url}` into a temp directory
2. `docker buildx build -f {dockerfile} -t {registry}/{project}:{sha} --push`
3. Report completion with `image_tag`

## Deploy Node

Requires:
- `docker` CLI
- `rsync` (for dependency datasets)
- NFS mounted at the same path as configured in each Dataset's `nfs_path`

On receiving a deploy task:

1. For each `dependency` dataset: rsync from NFS, symlink into deployment mount dir
2. For each `readwrite` dataset: prepare direct NFS bind-mount path
3. `docker rm -f muvee-{domain_prefix}` (rolling update: stop old container)
4. `docker run -d --name muvee-{domain_prefix} ... {image_tag}`
5. Report completion; update `node_datasets.last_used_at`
