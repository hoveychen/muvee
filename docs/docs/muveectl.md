---
id: muveectl
title: muveectl CLI
sidebar_position: 4
---

# muveectl – Muvee CLI

`muveectl` is the command-line client for Muvee. It lets you manage projects, datasets, and API tokens from your local machine without opening the web UI.

## Load the Skill into Your AI (Recommended)

muvee ships a built-in **Agent Skill** that teaches Cursor, Claude Code, Copilot, and other AI coding assistants everything they need to deploy your apps via `muveectl`. Once loaded, your AI can create projects, trigger deployments, and manage the platform — no manual commands required.

**Live skill URL** (pre-configured with your server address, no login needed):

```
https://YOUR_MUVEE_SERVER/api/skill
```

Copy this URL from the **Community page** of your muvee instance, or replace `YOUR_MUVEE_SERVER` with your domain.

**Install from GitHub** (available before you have a running server):

```bash
# macOS / Linux — global install for Cursor
curl -fsSL https://raw.githubusercontent.com/hoveychen/muvee/main/.cursor/skills/muveectl/SKILL.md \
  -o ~/.cursor/skills/muveectl/SKILL.md --create-dirs

# Claude Code
curl -fsSL https://raw.githubusercontent.com/hoveychen/muvee/main/.cursor/skills/muveectl/SKILL.md \
  -o ~/.claude/skills/muveectl/SKILL.md --create-dirs
```

The skill works across Cursor, Claude Code, GitHub Copilot, Windsurf, and any AI assistant that supports the [Agent Skills standard](https://skill.md/).

| Platform | Global directory | Project directory |
|----------|-----------------|-------------------|
| Cursor | `~/.cursor/skills/` | `.cursor/skills/` |
| Claude Code | `~/.claude/skills/` | `.claude/skills/` |
| Copilot | `~/.copilot/skills/` | `.github/skills/` |
| Windsurf | `~/.windsurf/skills/` | `.windsurf/skills/` |

:::tip One-shot deployment
After adding the skill, just tell your AI: *"Deploy my app at github.com/me/repo to my muvee server"* — it will install `muveectl`, log in, create the project, and deploy, all in one go.
:::

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
muveectl login --server https://example.com

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
muveectl projects metrics PROJECT_ID [--limit N]
muveectl projects workspace PROJECT_ID <ls|pull|push|rm> [args...]
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

### Container Metrics

The deploy agent collects resource usage from `docker stats` every ~15 seconds and reports it to the control plane. Use `projects metrics` to inspect a project's running container:

```bash
# Show the latest sample plus a history table (default: last 60 samples)
muveectl projects metrics PROJECT_ID

# Fetch up to 120 samples (~30 minutes of history)
muveectl projects metrics PROJECT_ID --limit 120
```

Each sample contains: `cpu_percent`, `mem_usage_bytes`, `mem_limit_bytes`, `net_rx_bytes`, `net_tx_bytes`, `block_read_bytes`, `block_write_bytes`, and `collected_at` (Unix epoch).

The maximum history retained per query is 1440 samples (~6 hours at 15-second intervals).

### Project Workspace

Each project can have a persistent **workspace volume** — an NFS-backed directory bind-mounted into the container. The mount path inside the container is configured per-project via the web UI (`volume_mount_path`, e.g. `/workspace`).

The control plane exposes a file management API so you can inspect and transfer workspace files without redeploying:

```bash
# List files in the workspace root (or a subdirectory)
muveectl projects workspace PROJECT_ID ls
muveectl projects workspace PROJECT_ID ls some/subdir

# Download a file from the workspace to the current directory
muveectl projects workspace PROJECT_ID pull data/output.csv

# Download and save with a specific local name
muveectl projects workspace PROJECT_ID pull data/output.csv ./local_copy.csv

# Upload a local file to the workspace root
muveectl projects workspace PROJECT_ID push ./model.bin

# Upload to a specific subdirectory (directory is created if it does not exist)
muveectl projects workspace PROJECT_ID push ./model.bin --remote-path models/

# Delete a file or directory (recursive)
muveectl projects workspace PROJECT_ID rm data/old_output.csv
muveectl projects workspace PROJECT_ID rm tmp/
```

:::info Prerequisite
The workspace feature requires `VOLUME_NFS_BASE_PATH` to be set on the control plane and the project's `volume_mount_path` to be configured. See [Configuration Reference](./configuration) for details.
:::

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

## Secrets

```bash
# List secrets (values never returned)
muveectl secrets list

# Create a password secret
muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx

# Create an SSH key secret
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# Delete a secret
muveectl secrets delete SECRET_ID
```

### Project Secret Bindings

```bash
# List project bindings
muveectl projects secrets PROJECT_ID

# Runtime env var injection
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --env-var GITHUB_TOKEN

# Git clone auth (HTTPS token)
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-git \
  --git-username x-access-token

# Build-time secret (docker buildx --secret)
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# --build-secret-id is optional; when omitted, muveectl derives it from the secret name
# e.g. "GITHUB_TOKEN" -> "github_token"

# Unbind
muveectl projects unbind-secret PROJECT_ID SECRET_ID
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--server URL` | Override the configured server URL for this call |
| `--json` | Output raw JSON (pipe-friendly) |

## Git Repository Requirements

For a project to deploy successfully, the repository must satisfy:

### Build
- Accessible via `git clone --depth=1` over HTTPS (public or with token secret) or SSH (SSH key secret)
- The configured branch must exist (default: `main`)
- A `Dockerfile` must exist at the configured path (default: `Dockerfile` in repo root)
- Image must build for **`linux/amd64`** (`docker buildx build --platform linux/amd64`)
- If private dependencies are required during build, bind a secret with `--use-for-build --build-secret-id <id>` and read it in Dockerfile via `/run/secrets/<id>`

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

# 4. Check container resource usage
muveectl projects metrics PROJECT_ID

# 5. Download a file produced by the container
muveectl projects workspace PROJECT_ID pull output/result.json
```
