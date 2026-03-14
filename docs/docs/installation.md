---
id: installation
title: Installation
sidebar_position: 2
---

# Installation

## Option A: Docker Compose (Recommended)

The quickest way to run the control plane. All services are pre-wired.

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
cp .env.example .env
# Edit .env with your values (BASE_DOMAIN, GOOGLE_*, JWT_SECRET, ADMIN_EMAILS,
#                              REGISTRY_USER, REGISTRY_PASSWORD, ACME_EMAIL)

# Generate registry/htpasswd before starting (required for the private registry)
docker run --entrypoint htpasswd httpd:2 -Bbn \
  "$REGISTRY_USER" "$REGISTRY_PASSWORD" > registry/htpasswd

docker network create muvee-net
docker compose up -d
```

## Option B: Pre-built Binaries

Download from the [Releases page](https://github.com/hoveychen/muvee/releases):

```bash
# Linux amd64
curl -L https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64.tar.gz | tar xz
chmod +x muvee_linux_amd64

# Run server (serves web UI on :8080)
export DATABASE_URL="postgres://..."
export GOOGLE_CLIENT_ID="..."
./muvee_linux_amd64 server
```

The `muvee server` subcommand includes the React web UI — no separate static file server needed.

## Option C: Build from Source

Requirements: Go 1.26+, Node 22+

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee
make build   # builds frontend + embeds it + compiles single binary
ls bin/
# muvee
```

## Agents

Run `muvee agent` on each worker node. A single binary handles both builder and deploy roles.

Both builder **and** deploy nodes need registry credentials: builder pushes images after building, and deploy pulls images to start containers.

:::caution Use an internal address for CONTROL_PLANE_URL
Set `CONTROL_PLANE_URL` to the **internal network address** of the control plane, not the public-facing domain. This ensures the agent's `HOST_IP` is auto-detected correctly (the IP Traefik uses to reach the container).
:::

```bash
# Builder node
NODE_ROLE=builder \
CONTROL_PLANE_URL=http://10.0.0.1:8080 \
AGENT_SECRET=your-agent-secret \
REGISTRY_ADDR=registry.example.com \
REGISTRY_USER=registry-user \
REGISTRY_PASSWORD=a-strong-password \
./muvee agent

# Deploy node
NODE_ROLE=deploy \
CONTROL_PLANE_URL=http://10.0.0.1:8080 \
AGENT_SECRET=your-agent-secret \
REGISTRY_ADDR=registry.example.com \
REGISTRY_USER=registry-user \
REGISTRY_PASSWORD=a-strong-password \
DATA_DIR=/muvee/data \
BASE_DOMAIN=example.com \
./muvee agent
```

Agents register themselves with the control plane and poll for tasks every 5 seconds. They require Docker to be installed and accessible on the host. On startup the agent automatically runs `docker login <REGISTRY_ADDR>` with the provided credentials.
