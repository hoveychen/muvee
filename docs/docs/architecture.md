---
id: architecture
title: Architecture
sidebar_position: 4
---

# Architecture

## Overview

![muvee system architecture overview](/img/arch-overview-en.png)

```
┌─────────────────────────────────────────────┐
│              Control Plane                   │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  │
│  │API Server│  │Scheduler │  │  Monitor  │  │
│  │ (Go+Chi) │  │Affinity  │  │NFS Poller │  │
│  └────┬─────┘  └────┬─────┘  └───────────┘  │
│       │             │                         │
│  ┌────▼─────────────▼──┐  ┌───────────────┐  │
│  │     PostgreSQL       │  │  Distribution │  │
│  │   (metadata DB)      │  │   Registry    │  │
│  └─────────────────────┘  └───────────────┘  │
│  ┌──────────────────────────────────────────┐ │
│  │   Traefik v3  (reverse proxy + HTTPS)    │ │
│  └──────────────────────────────────────────┘ │
└─────────────────────────────────────────────┘
          │ long-poll tasks            │ push image
          ▼                            ▼
┌──────────────────┐        ┌──────────────────┐
│   Builder Nodes   │        │   Deploy Nodes    │
│  git clone        │        │  rsync NFS→local  │
│  docker buildx    │        │  LRU cache        │
│  push image       │        │  docker run       │
└──────────────────┘        └──────────────────┘
                                       │
                              ┌────────▼────────┐
                              │  NFS Data Store  │
                              │  /nfs/warehouse  │
                              └─────────────────┘
```

## Component Breakdown

### Control Plane (`cmd/server`)

- **API Server** (`internal/api`) — Chi router, JWT auth middleware, REST handlers for Projects, Datasets, Deployments, Nodes, Users
- **Git Hosting** (`internal/gitrepo`) — Bare git repository lifecycle, Git Smart HTTP protocol handler (`git-upload-pack` / `git-receive-pack`), repository browser (tree, blob, commits, branches)
- **Scheduler** (`internal/scheduler`) — Affinity scoring, LRU eviction trigger, task dispatch
- **Monitor** (`internal/monitor`) — Periodic NFS path scan, checksum diff, file history recording
- **Auth** (`internal/auth`) — Google OIDC, JWT signing, RBAC middleware, project-scoped API tokens

### Agent (`cmd/agent`)

A single binary deployed on worker nodes. Role is configured via `NODE_ROLE` env var:

- **builder** — polls for build tasks, runs `git clone` + `docker buildx build` + `docker push`
- **deploy** — polls for deploy tasks, runs data sync (rsync / NFS bind-mount), `docker run -p 0:{port}`, reports `host_port` back to the control plane

### ForwardAuth Service (`cmd/authservice`)

Standalone HTTP service that Traefik calls via ForwardAuth middleware to enforce per-project Google authentication.

## Data Flow: Deployment

![muvee deployment flow](/img/deploy-flow-en.png)

```
User clicks "Deploy"
    │
    ▼
API creates Deployment record (status=pending)
    │
    ▼
Scheduler.DispatchBuild()
  → picks active builder node
  → creates build Task
    │
    ▼
Builder Agent polls & picks up task
  → git clone --depth=1 (external URL or hosted repo via internal HTTP)
  → docker buildx build
  → docker push registry/{project}:{sha}
  → POST /api/agent/tasks/{id}/complete
    │
    ▼
Server background loop detects completed build
  → Scheduler.DispatchDeploy()
  → scores deploy nodes by affinity
  → creates deploy Task
    │
    ▼
Deploy Agent picks up task
  → For each dependency dataset:
      rsync NFS → /muvee/data/objects/{id}/v{ver}
      symlink → /muvee/data/mounts/{deployment_id}/{name}
  → For each readwrite dataset:
      NFS bind-mount directly
  → docker run -p 0:{container_port} -v /muvee/data/mounts/...
  → docker port → discover assigned host_port
  → POST /api/agent/tasks/{id}/complete { host_port }
    │
    ▼
Control plane stores host_ip + host_port in deployments table
    │
    ▼
Traefik HTTP provider polls GET /api/traefik/config (every 5s)
  → {project}.domain.com → http://{node_ip}:{host_port}
```

## Affinity Scoring & Dataset Modes

![dataset scheduling and mount modes](/img/dataset-scheduling-en.png)

When scheduling a deploy task, each active deploy node receives a score:

```
score(node) =
  + cached_dataset_count × 10       # minimize rsync cost
  - missing_bytes × 0.000001        # penalize large syncs
  + free_storage_bytes × 0.0000001  # prefer roomier nodes
```

If the best node lacks sufficient free space, the LRU dataset cache is evicted first (oldest `last_used_at` first).

## Dataset Modes

| Mode | Storage | LRU | NFS dependency | Container mount |
|---|---|---|---|---|
| `dependency` | Local (rsync) | Yes | Read path only | `:ro` bind-mount of local copy |
| `readwrite` | None | No | Direct at runtime | `:rw` bind-mount of NFS path |
