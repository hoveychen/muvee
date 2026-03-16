---
id: install-agent
title: Agent Installation
sidebar_position: 9
---

# Agent Installation

A muvee agent runs on a worker node and polls the control plane for build or deploy tasks. This guide walks through setting up a brand-new machine — from installing dependencies to registering the node in the cluster — for Linux, macOS, and Windows.

## Before You Start

You need the following values from your control plane:

| Value | Where to find it |
|-------|-----------------|
| `CONTROL_PLANE_URL` | Internal network address of the control plane, e.g. `http://10.0.0.1:8080` |
| `AGENT_SECRET` | The value set in `.env` on the control plane |
| `NODE_ROLE` | `builder` or `deploy` |

:::caution Use the internal address
Set `CONTROL_PLANE_URL` to the **internal network address** of the control plane, not the public domain. The agent uses this to auto-detect its own `HOST_IP` so Traefik can route traffic back to deployed containers.
:::

---

## Linux

### Install dependencies

```bash
# Debian / Ubuntu
sudo apt-get update
sudo apt-get install -y docker.io docker-buildx git rsync

sudo systemctl enable --now docker
sudo usermod -aG docker $USER
newgrp docker
```

### Option A — Docker (recommended for Linux)

```bash
# Builder node
docker run -d --name muvee-agent --restart unless-stopped \
  -e NODE_ROLE=builder \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=<your-agent-secret> \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/hoveychen/muvee:latest agent

# Deploy node
docker run -d --name muvee-agent --restart unless-stopped \
  -e NODE_ROLE=deploy \
  -e CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  -e AGENT_SECRET=<your-agent-secret> \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /muvee/data:/muvee/data \
  -v /mnt/nfs/volumes:/mnt/nfs/volumes \
  -v /mnt/nfs/datasets:/mnt/nfs/datasets \
  ghcr.io/hoveychen/muvee:latest agent
```

The container mounts `/var/run/docker.sock` so the agent can control the **host** Docker daemon (Docker-outside-of-Docker). Deployed containers are siblings of the agent container, not nested inside it.

### Option B — Binary

```bash
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/

NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/muvee/data \
  muvee agent
```

To run as a systemd service:

```ini title="/etc/systemd/system/muvee-agent.service"
[Unit]
Description=muvee agent
After=network.target docker.service
Requires=docker.service

[Service]
Environment=NODE_ROLE=deploy
Environment=CONTROL_PLANE_URL=http://10.0.0.1:8080
Environment=AGENT_SECRET=REPLACE_WITH_SECRET
Environment=DATA_DIR=/muvee/data
ExecStart=/usr/local/bin/muvee agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now muvee-agent
```

---

## macOS

Docker Desktop on macOS runs containers inside a Linux VM. The `docker.sock` inside a container points to the VM's Docker daemon — containers deployed by the agent would be unreachable by Traefik. Use the **native binary** instead.

### Install dependencies

```bash
# Homebrew (if not already installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Docker Desktop — provides docker CLI and buildx
brew install --cask docker
open /Applications/Docker.app   # complete onboarding, then verify:
docker version

# git and rsync
xcode-select --install   # rsync is pre-installed on macOS; git comes with Xcode CLT
```

### Download the binary

```bash
# Apple Silicon (M1 / M2 / M3)
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_darwin_arm64
chmod +x muvee && sudo mv muvee /usr/local/bin/

# Intel Mac
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_darwin_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/
```

### Start the agent

```bash
# Builder node
NODE_ROLE=builder \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  muvee agent

# Deploy node
NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/Users/Shared/muvee/data \
  muvee agent
```

### Run as a background service (launchd)

```xml title="/Library/LaunchDaemons/com.muvee.agent.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.muvee.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/muvee</string>
    <string>agent</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>NODE_ROLE</key><string>deploy</string>
    <key>CONTROL_PLANE_URL</key><string>http://10.0.0.1:8080</string>
    <key>AGENT_SECRET</key><string>REPLACE_WITH_SECRET</string>
    <key>DATA_DIR</key><string>/Users/Shared/muvee/data</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/muvee-agent.log</string>
  <key>StandardErrorPath</key><string>/var/log/muvee-agent.log</string>
</dict>
</plist>
```

```bash
sudo launchctl load /Library/LaunchDaemons/com.muvee.agent.plist
```

---

## Windows

The recommended approach is to run the agent inside **WSL2**, which provides a full Linux environment with access to the Docker Desktop daemon.

### 1 — Install WSL2

Open PowerShell as Administrator:

```powershell
wsl --install
```

This installs WSL2 with Ubuntu. Restart when prompted.

### 2 — Install Docker Desktop

1. Download and install [Docker Desktop for Windows](https://www.docker.com/products/docker-desktop/).
2. In Docker Desktop → **Settings → General**: enable **Use the WSL 2 based engine**.
3. In Docker Desktop → **Settings → Resources → WSL Integration**: enable your Ubuntu distro.

Verify from the WSL2 terminal:

```bash
docker version
```

### 3 — Install dependencies inside WSL2

```bash
sudo apt-get update
sudo apt-get install -y git rsync
# docker CLI is provided by Docker Desktop via WSL integration
```

### 4 — Download the binary

```bash
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/
```

### 5 — Start the agent

```bash
# Deploy node (inside WSL2)
NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/mnt/c/muvee/data \
  muvee agent
```

Windows paths are accessible under `/mnt/c/...` inside WSL2.

### 6 — Run as a background service

Add to `/etc/wsl.conf` inside WSL2:

```ini title="/etc/wsl.conf"
[boot]
command = service muvee-agent start
```

Create the init script:

```bash
sudo tee /etc/init.d/muvee-agent > /dev/null <<'EOF'
#!/bin/sh
export NODE_ROLE=deploy
export CONTROL_PLANE_URL=http://10.0.0.1:8080
export AGENT_SECRET=REPLACE_WITH_SECRET
export DATA_DIR=/mnt/c/muvee/data
exec /usr/local/bin/muvee agent >> /var/log/muvee-agent.log 2>&1
EOF
sudo chmod +x /etc/init.d/muvee-agent
sudo update-rc.d muvee-agent defaults
```

Restart WSL2 to apply: `wsl --shutdown` then reopen the terminal.

---

## Verify Registration

After starting the agent, confirm it appears in the control plane:

```bash
muveectl nodes list
```

The node should show as **online** within 30 seconds. If it stays offline:

1. Verify `CONTROL_PLANE_URL` is reachable: `curl http://10.0.0.1:8080/healthz`
2. Verify `AGENT_SECRET` matches the control plane `.env`
3. Verify Docker is running: `docker version`
4. Check agent logs for errors

---

## Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `NODE_ROLE` | ✓ | — | `builder` or `deploy` |
| `CONTROL_PLANE_URL` | ✓ | `http://localhost:8080` | Internal address of the control plane |
| `AGENT_SECRET` | ✓ | — | Shared secret (must match control plane) |
| `DATA_DIR` | deploy only | `/muvee/data` | Local directory for dataset cache |
| `HOST_IP` | — | auto-detected | Override the IP reported to Traefik |
