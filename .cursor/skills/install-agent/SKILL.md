---
name: install-agent
description: Install and register a muvee worker agent (builder or deploy role) on a fresh machine. Covers dependency setup (Docker, git, rsync), binary download, and startup for Linux, macOS, and Windows (WSL2). Use when the user wants to add a new node to their muvee cluster.
---

# Install muvee Agent

A muvee agent runs on a worker node and polls the control plane for build or deploy tasks. This skill walks through setting up a brand-new machine — from installing dependencies to registering the agent with the cluster.

## Prerequisites

You need the following from your muvee control plane:

| Value | Where to find it |
|-------|-----------------|
| `CONTROL_PLANE_URL` | Internal network address of the control plane, e.g. `http://10.0.0.1:8080` |
| `AGENT_SECRET` | The value set in `.env` on the control plane |
| `NODE_ROLE` | `builder` or `deploy` |

> Always use the **internal** address for `CONTROL_PLANE_URL`, not the public domain.

---

## Linux

### 1 — Install dependencies

```bash
# Debian / Ubuntu
sudo apt-get update
sudo apt-get install -y docker.io docker-buildx git rsync

# Enable Docker and add current user
sudo systemctl enable --now docker
sudo usermod -aG docker $USER
newgrp docker
```

For other distributions (Fedora, Arch, etc.) use the equivalent package manager.

### 2 — Run the agent (Docker)

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
  -v /nfs/warehouse:/nfs/warehouse \
  ghcr.io/hoveychen/muvee:latest agent
```

### 2 — Alternative: run as binary

```bash
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/

NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/muvee/data \
  muvee agent
```

---

## macOS

Docker Desktop on macOS runs containers inside a Linux VM. The `docker.sock` inside a container points to the VM's Docker daemon, not the host's — containers deployed by the agent would be invisible to Traefik. Use the **native binary** instead.

### 1 — Install dependencies

```bash
# Install Homebrew if not already present
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Docker Desktop (provides docker CLI + buildx)
brew install --cask docker
open /Applications/Docker.app   # start Docker Desktop and complete onboarding

# git and rsync (rsync is pre-installed on macOS; git via Xcode CLT)
xcode-select --install
```

### 2 — Download the binary

```bash
# Apple Silicon (M1/M2/M3)
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_darwin_arm64
chmod +x muvee && sudo mv muvee /usr/local/bin/

# Intel Mac
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_darwin_amd64
chmod +x muvee && sudo mv muvee /usr/local/bin/
```

### 3 — Start the agent

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

### 4 — Run as a background service (launchd)

```bash
# Create a launchd plist (deploy node example)
sudo tee /Library/LaunchDaemons/com.muvee.agent.plist > /dev/null <<'EOF'
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
EOF

sudo launchctl load /Library/LaunchDaemons/com.muvee.agent.plist
```

---

## Windows

The recommended approach is to run the agent inside **WSL2**, which provides a full Linux environment with access to Docker Desktop's daemon.

### 1 — Install WSL2 and Docker Desktop

1. Open PowerShell as Administrator and run:
   ```powershell
   wsl --install
   ```
   This installs WSL2 with Ubuntu by default. Restart when prompted.

2. Install [Docker Desktop for Windows](https://www.docker.com/products/docker-desktop/).

3. In Docker Desktop → **Settings → General**: enable **Use the WSL 2 based engine**.

4. In Docker Desktop → **Settings → Resources → WSL Integration**: enable your Ubuntu distro.

### 2 — Install dependencies inside WSL2

Open the Ubuntu WSL2 terminal:

```bash
sudo apt-get update
sudo apt-get install -y git rsync
# docker CLI is provided by Docker Desktop via WSL integration — no separate install needed
docker version   # verify it works
```

### 3 — Download the binary

```bash
# Inside WSL2
curl -Lo muvee https://github.com/hoveychen/muvee/releases/latest/download/muvee_linux_amd64
chmod +x muvee
sudo mv muvee /usr/local/bin/
```

### 4 — Start the agent

```bash
# Inside WSL2 — Deploy node example
NODE_ROLE=deploy \
  CONTROL_PLANE_URL=http://10.0.0.1:8080 \
  AGENT_SECRET=<your-agent-secret> \
  DATA_DIR=/mnt/c/muvee/data \
  muvee agent
```

Windows paths are accessible under `/mnt/c/...` in WSL2.

### 5 — Run as a background service

Create a startup script that WSL2 runs automatically:

```bash
# /etc/init.d/muvee-agent  (inside WSL2)
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

Then configure WSL2 to start services on Windows boot by adding to `/etc/wsl.conf`:

```ini
[boot]
command = service muvee-agent start
```

---

## Verify Registration

After starting the agent, confirm it appears in the control plane:

```bash
muveectl nodes list
```

The node should show as **online** within 30 seconds. If it stays offline, check:

1. `CONTROL_PLANE_URL` is reachable from the agent machine
2. `AGENT_SECRET` matches the control plane setting
3. Docker is running: `docker version`
4. Agent logs for error messages

## Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `NODE_ROLE` | ✓ | — | `builder` or `deploy` |
| `CONTROL_PLANE_URL` | ✓ | `http://localhost:8080` | Internal address of the control plane |
| `AGENT_SECRET` | ✓ | — | Shared secret for agent authentication |
| `DATA_DIR` | deploy only | `/muvee/data` | Local directory for dataset cache |
| `HOST_IP` | — | auto-detected | Override the IP reported to Traefik |
