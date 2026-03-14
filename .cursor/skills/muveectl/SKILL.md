---
name: muveectl
description: Operate the Muvee self-hosted PaaS via the muveectl CLI. Manages projects (create, update, deploy, delete), datasets (create, scan, delete), and API tokens. Use when the user wants to interact with their Muvee server from the command line, trigger deployments, or manage infrastructure resources.
---

# muveectl – Muvee CLI

## Installation

Download the latest binary from [GitHub Releases](https://github.com/hoveychen/muvee/releases/latest):

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
# Move to a directory in your PATH, e.g.:
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

When enabled, Traefik intercepts every request and redirects unauthenticated users to Google OAuth login before forwarding to the container.

- `--auth-required` — enable protection
- `--no-auth` — disable protection
- `--auth-domains example.com,corp.com` — restrict to specific email domains (optional; omit to allow all Google accounts)

The authenticated user's email is forwarded to the container via the **`X-Forwarded-User`** HTTP header. Read it server-side to identify the current user:

```python
# Python / Flask example
user_email = request.headers.get("X-Forwarded-User")  # e.g. "alice@example.com"
```

```go
// Go example
userEmail := r.Header.Get("X-Forwarded-User")
```

```typescript
// Node.js / Express example
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
muveectl tokens create [--name NAME]   # token value shown once on creation
muveectl tokens delete TOKEN_ID
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--server URL` | Override the configured server URL for this call |
| `--json` | Output raw JSON (pipe-friendly) |

## Git Repository Requirements

For a project to deploy successfully the repository must satisfy:

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

1. Get project IDs: `muveectl projects list --json`
2. Deploy a project: `muveectl projects deploy PROJECT_ID`
3. Check deployment status: `muveectl projects deployments PROJECT_ID`
