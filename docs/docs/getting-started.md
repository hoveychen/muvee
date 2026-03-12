---
id: getting-started
title: Getting Started
sidebar_position: 1
---

# Getting Started

muvee is a lightweight self-hosted PaaS that turns any private cloud into a container deployment platform — with built-in data warehouse integration, LRU-cached dataset injection, and per-project Google authentication.

## Prerequisites

| Requirement | Notes |
|---|---|
| Linux hosts | Control plane + agent nodes |
| Docker + Docker Buildx | On all nodes |
| NFS share | Mounted at the same path on all deploy nodes |
| PostgreSQL 16+ | Can run in Docker (included in `docker-compose.yml`) |
| Google OAuth2 credentials | From [Google Cloud Console](https://console.cloud.google.com/apis/credentials) |

## 5-Minute Quickstart

### 1. Clone and configure

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
cp .env.example .env
```

Edit `.env`:

```env
GOOGLE_CLIENT_ID=your-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-secret
ALLOWED_DOMAINS=your-company.com
BASE_DOMAIN=example.com
JWT_SECRET=your-random-secret
ACME_EMAIL=admin@example.com
```

### 2. Start the control plane

```bash
# Create the Docker network first
docker network create muvee-net

# Start all services
docker compose up -d
```

This starts:
- **muvee-server** — API + embedded web UI (`:8080`)
- **muvee-authservice** — Traefik ForwardAuth sidecar (`:4181`)
- **PostgreSQL** — metadata store
- **Traefik** — reverse proxy with automatic HTTPS (`:80`, `:443`)
- **Registry** — private Docker image registry

### 3. Connect agent nodes

On each worker node (builder or deploy), run:

```bash
# Builder node
docker run -d --name muvee-agent \
  -e NODE_ROLE=builder \
  -e CONTROL_PLANE_URL=https://www.example.com \
  -e REGISTRY_ADDR=registry.example.com \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/hoveychen/muvee:latest agent

# Deploy node
docker run -d --name muvee-agent \
  -e NODE_ROLE=deploy \
  -e CONTROL_PLANE_URL=https://www.example.com \
  -e DATA_DIR=/muvee/data \
  -e BASE_DOMAIN=example.com \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /muvee/data:/muvee/data \
  -v /nfs/warehouse:/nfs/warehouse \
  ghcr.io/hoveychen/muvee:latest agent
```

### 4. Create your first project

1. Open `https://www.example.com` and sign in with Google
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
