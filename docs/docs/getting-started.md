---
id: getting-started
title: Getting Started
sidebar_position: 1
---

# Getting Started

muvee is a lightweight self-hosted PaaS that turns any private cloud into a container deployment platform — with built-in data warehouse integration, LRU-cached dataset injection, and flexible identity provider support.

## Prerequisites

| Requirement | Notes |
|---|---|
| Linux hosts | Control plane + agent nodes |
| Docker + Docker Buildx | On all nodes |
| NFS share | Mounted at the same path on all deploy nodes |
| PostgreSQL 16+ | Can run in Docker (included in `docker-compose.yml`) |
| Identity provider | At least one of: [Google](./auth/auth-google), [Feishu/Lark](./auth/auth-feishu), [WeCom](./auth/auth-wecom), [DingTalk](./auth/auth-dingtalk) |

## 5-Minute Quickstart

### 1. Clone and configure

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
cp .env.example .env
```

Edit `.env` and configure at least one identity provider. Example with Google:

```env
GOOGLE_CLIENT_ID=your-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-secret
ALLOWED_DOMAINS=your-company.com
ADMIN_EMAILS=admin@your-company.com
BASE_DOMAIN=example.com
JWT_SECRET=your-random-secret
ACME_EMAIL=admin@example.com
REGISTRY_USER=registry-user
REGISTRY_PASSWORD=a-strong-password
AGENT_SECRET=your-agent-secret   # shared secret between server and all agents
```

See the [Authentication](./auth/auth-google) section for setup guides for each supported provider. Multiple providers can be enabled simultaneously — the login page shows a button for each one.

`ADMIN_EMAILS` is a comma-separated list of email addresses that receive the `admin` role on login and can access the Traefik dashboard at `https://traefik.BASE_DOMAIN`.

`AGENT_SECRET` protects the `/api/agent/*` endpoints. Generate with `openssl rand -hex 32` and use the same value on the server and all agent nodes.

### 2. Generate registry credentials

The private Docker registry uses htpasswd Basic Auth. Run once on the control plane host **before** starting Docker Compose:

```bash
docker run --entrypoint htpasswd httpd:2 -Bbn \
  "$REGISTRY_USER" "$REGISTRY_PASSWORD" > registry/htpasswd
```

### 3. Start the control plane

```bash
# Create the Docker network first
docker network create muvee-net

# Start all services
docker compose up -d
```

This starts:
- **muvee-server** — API + embedded web UI at `https://BASE_DOMAIN`
- **muvee-authservice** — Traefik ForwardAuth sidecar (`:4181`)
- **PostgreSQL** — metadata store
- **Traefik** — reverse proxy with automatic HTTPS; dashboard at `https://traefik.BASE_DOMAIN` (admin only)
- **Registry** — private Docker image registry at `https://registry.BASE_DOMAIN` (htpasswd auth)

### 4. Connect agent nodes

Registry credentials (`REGISTRY_ADDR`, `REGISTRY_USER`, `REGISTRY_PASSWORD`) and `BASE_DOMAIN` are **automatically distributed** from the control plane. Agents call `GET /api/agent/config` on startup and run `docker login` using the values returned — no per-node credential configuration needed.

> **Important:** `CONTROL_PLANE_URL` must be the **internal network address** of the control plane (not the public domain). The agent uses this address to auto-detect which network interface — and IP — Traefik should use to reach deployed containers.

```bash
# Builder node
docker run -d --name muvee-agent \
  -e NODE_ROLE=builder \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=your-agent-secret \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/hoveychen/muvee:latest agent

# Deploy node
docker run -d --name muvee-agent \
  -e NODE_ROLE=deploy \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=your-agent-secret \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /muvee/data:/muvee/data \
  -v /nfs/warehouse:/nfs/warehouse \
  ghcr.io/hoveychen/muvee:latest agent
```

### 5. Create your first project

1. Open `https://example.com` and sign in with your configured identity provider
2. Click **New Project**
3. Fill in Git URL, branch, domain prefix
4. Click **Deploy** — muvee will:
   - Clone your repo on a builder node
   - Build the Dockerfile
   - Push the image to the internal registry
   - Deploy the container on a deploy node
   - Configure Traefik routing to `{domain_prefix}.example.com`

## Download Binaries

Pre-built binaries for Linux and macOS (amd64 + arm64) are available on the [Releases page](https://github.com/hoveychen/muvee/releases).

```bash
# Example: download and run on Linux amd64
curl -L https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64.tar.gz | tar xz
./muvee_linux_amd64 server
```

The `muvee server` subcommand includes the full React web UI — no separate web server needed.
