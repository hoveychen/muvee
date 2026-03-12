---
id: architecture
title: Architecture
sidebar_position: 4
---

# Architecture

## Overview

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
- **Scheduler** (`internal/scheduler`) — Affinity scoring, LRU eviction trigger, task dispatch
- **Monitor** (`internal/monitor`) — Periodic NFS path scan, checksum diff, file history recording
- **Auth** (`internal/auth`) — Google OIDC, JWT signing, RBAC middleware

### Agent (`cmd/agent`)

A single binary deployed on worker nodes. Role is configured via `NODE_ROLE` env var:

- **builder** — polls for build tasks, runs `git clone` + `docker buildx build` + `docker push`
- **deploy** — polls for deploy tasks, runs data sync (rsync / NFS bind-mount), `docker run` with Traefik labels

### ForwardAuth Service (`cmd/authservice`)

Standalone HTTP service that Traefik calls via ForwardAuth middleware to enforce per-project Google authentication.

## Data Flow: Deployment

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
  → git clone --depth=1
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
  → docker run
      -v /muvee/data/mounts/... 
      -l traefik.http.routers.{proj}...
    │
    ▼
Traefik detects new container labels
  → {project}.domain.com now routes to container
```

## Affinity Scoring

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
