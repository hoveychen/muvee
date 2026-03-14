---
id: muveectl
title: muveectl CLI
sidebar_position: 4
---

# muveectl – Muvee CLI

`muveectl` is the command-line client for Muvee. It lets you manage projects, datasets, and API tokens from your local machine without opening the web UI.

## Installation

Download the latest binary from the [Releases page](https://github.com/hoveychen/muvee/releases/latest):

**macOS (Apple Silicon)**
```bash
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**macOS (Intel)**
```bash
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_darwin_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**Linux (amd64)**
```bash
curl -Lo muveectl https://github.com/hoveychen/muvee/releases/latest/download/muveectl_linux_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**Windows (PowerShell)**
```powershell
Invoke-WebRequest -Uri https://github.com/hoveychen/muvee/releases/latest/download/muveectl_windows_amd64.exe -OutFile muveectl.exe
Move-Item muveectl.exe "$env:LOCALAPPDATA\Microsoft\WindowsApps\muveectl.exe"
```

## Authentication

```bash
# First-time login (opens browser for Google OAuth)
muveectl login --server https://www.example.com

# Verify session
muveectl whoami
```

Config is saved at `~/.config/muveectl/config.json`. All subsequent commands use the stored server and token automatically.

## Projects

```bash
muveectl projects list
muveectl projects create --name NAME --git-url URL \
  [--branch BRANCH] [--domain PREFIX] [--dockerfile PATH] \
  [--auth-required] [--auth-domains example.com,corp.com]
muveectl projects get PROJECT_ID
muveectl projects update PROJECT_ID [--branch BRANCH] [--auth-required] [--no-auth] [--auth-domains DOMAINS]
muveectl projects deploy PROJECT_ID
muveectl projects deployments PROJECT_ID
muveectl projects delete PROJECT_ID
```

### Google OAuth protection (`--auth-required`)

When enabled, Traefik intercepts every request and redirects unauthenticated users to Google OAuth before forwarding to the container.

| Flag | Description |
|------|-------------|
| `--auth-required` | Enable per-project Google auth |
| `--no-auth` | Disable per-project Google auth |
| `--auth-domains example.com,corp.com` | Restrict to specific email domains (omit to allow all Google accounts) |

The authenticated user's email is forwarded to the container via the `X-Forwarded-User` HTTP header:

```python
# Python / Flask
user_email = request.headers.get("X-Forwarded-User")
```

```go
// Go
userEmail := r.Header.Get("X-Forwarded-User")
```

```typescript
// Node.js / Express
const userEmail = req.headers["x-forwarded-user"]
```

## Datasets

```bash
muveectl datasets list
muveectl datasets create --name NAME --nfs-path NFS_PATH
muveectl datasets get DATASET_ID
muveectl datasets scan DATASET_ID
muveectl datasets delete DATASET_ID
```

## API Tokens

```bash
muveectl tokens list
muveectl tokens create [--name NAME]   # token value is shown once on creation
muveectl tokens delete TOKEN_ID
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--server URL` | Override the configured server URL for this call |
| `--json` | Output raw JSON (pipe-friendly) |

## Git Repository Requirements

For a project to deploy successfully, the repository must satisfy:

### Build
- Accessible via `git clone --depth=1` over HTTPS (public) or SSH (builder node must have the key)
- The configured branch must exist (default: `main`)
- A `Dockerfile` must exist at the configured path (default: `Dockerfile` in repo root)
- Image must build for **`linux/amd64`** (`docker buildx build --platform linux/amd64`)

### Runtime
- Container must serve **HTTP** on port **8080** — Traefik handles TLS termination
- Do not start HTTPS inside the container
- App will be reachable at `https://<domain_prefix>.<base_domain>`

### Dataset mounts

Datasets are injected as Docker volumes at `/data/<dataset_name>`:

| Mode | Access |
|------|--------|
| `dependency` | Read-only — rsync-cached local copy |
| `readwrite` | Read-write — direct NFS mount |

## Typical Workflow

```bash
# 1. List projects and grab IDs
muveectl projects list --json

# 2. Deploy a project
muveectl projects deploy PROJECT_ID

# 3. Monitor deployment progress
muveectl projects deployments PROJECT_ID
```
