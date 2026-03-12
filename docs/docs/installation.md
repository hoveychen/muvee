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
# Edit .env with your values
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

Run `muvee agent` on each worker node. A single binary handles both builder and deploy roles:

```bash
# Builder node
NODE_ROLE=builder \
CONTROL_PLANE_URL=https://www.example.com \
REGISTRY_ADDR=registry.example.com \
./muvee agent

# Deploy node
NODE_ROLE=deploy \
CONTROL_PLANE_URL=https://www.example.com \
DATA_DIR=/muvee/data \
BASE_DOMAIN=example.com \
AUTH_SERVICE_URL=http://muvee-authservice:4181/verify \
./muvee agent
```

Agents register themselves with the control plane and poll for tasks every 5 seconds. They require Docker to be installed and accessible on the host.
