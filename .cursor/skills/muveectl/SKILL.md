---
name: muveectl
version: 1
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
# --dockerfile PATH: path to the Dockerfile *file* relative to the repo root
#   default: "Dockerfile"  (repo root Dockerfile)
#   example: "web/Dockerfile" for a subdirectory
#   WRONG: "." or "web/" — must be a file path, not a directory
muveectl projects get PROJECT_ID
muveectl projects update PROJECT_ID [--branch BRANCH] [--auth-required] [--no-auth] [--auth-domains DOMAINS]
muveectl projects deploy PROJECT_ID
muveectl projects deployments PROJECT_ID
muveectl projects metrics PROJECT_ID [--limit N]
muveectl projects port-forward PROJECT_ID [--port PORT]
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

### Container Metrics

```bash
# Show the latest resource sample and historical table
muveectl projects metrics PROJECT_ID

# Fetch more history (default: 60 samples)
muveectl projects metrics PROJECT_ID --limit 120
```

Fields returned per sample: `collected_at`, `cpu_percent`, `mem_usage_bytes`, `mem_limit_bytes`, `net_rx_bytes`, `net_tx_bytes`, `block_read_bytes`, `block_write_bytes`.

### Workspace

The workspace is persistent storage attached to the running container, accessible at `/workspace` inside the container. Use these commands to inspect and transfer files without redeploying.

```bash
# List files in the workspace root (or a subdirectory)
muveectl projects workspace PROJECT_ID ls
muveectl projects workspace PROJECT_ID ls some/subdir

# Download a file from the workspace
muveectl projects workspace PROJECT_ID pull remote/path/file.txt
muveectl projects workspace PROJECT_ID pull remote/path/file.txt local_copy.txt

# Upload a local file to the workspace
muveectl projects workspace PROJECT_ID push local_file.bin
muveectl projects workspace PROJECT_ID push local_file.bin --remote-path uploads/file.bin

# Delete a file from the workspace
muveectl projects workspace PROJECT_ID rm remote/path/file.txt
```

## Local Port Forwarding

Forward a project's running container to a local port for development. Authentication is automatically handled using your CLI identity — the container receives your email in the `X-Forwarded-User` header, just like in production.

```bash
# Auto-pick a free local port
muveectl projects port-forward PROJECT_ID

# Use a specific local port
muveectl projects port-forward PROJECT_ID --port 3000
```

Then call the project's API locally:

```bash
curl http://127.0.0.1:3000/api/some-endpoint
```

This is useful for local development when your code needs to call APIs exposed by a deployed project, without dealing with OAuth login flows or TLS certificates.

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

## Secrets

Secrets store passwords, API tokens, and SSH private keys — encrypted at rest (AES-256-GCM). Values are **write-only** and never returned after creation.

```bash
# List secrets (names and types only)
muveectl secrets list

# Create a password/token secret
muveectl secrets create --name GITHUB_TOKEN --type password --value ghp_xxxxx

# Create an SSH key secret from a file (for private git repos)
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# Delete a secret
muveectl secrets delete SECRET_ID
```

### Binding Secrets to Projects

Secrets can be used in three ways:
- Runtime env vars (`--env-var`)
- Git clone auth (`--use-for-git`)
- Docker build-time secret mounts (`--use-for-build --build-secret-id`)

```bash
# List secrets bound to a project
muveectl projects secrets PROJECT_ID

# Bind a secret as an environment variable
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --env-var GITHUB_TOKEN

# Bind a password secret for HTTPS git auth (GitHub fine-grained PAT)
muveectl projects bind-secret PROJECT_ID \
  --secret-id TOKEN_SECRET_ID \
  --use-for-git \
  --git-username x-access-token   # default; for GitLab use "oauth2"

# Bind a secret for docker buildx secret mount
muveectl projects bind-secret PROJECT_ID \
  --secret-id TOKEN_SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# --build-secret-id is optional; muveectl auto-derives it from secret name
# e.g. "GITHUB_TOKEN" -> "github_token"

# Bind an SSH key for git clone
muveectl projects bind-secret PROJECT_ID \
  --secret-id SSH_KEY_SECRET_ID \
  --use-for-git

# Bind a secret for BOTH git auth AND as runtime env var
muveectl projects bind-secret PROJECT_ID \
  --secret-id TOKEN_SECRET_ID \
  --env-var GITHUB_TOKEN \
  --use-for-git \
  --git-username x-access-token

# Remove a secret binding
muveectl projects unbind-secret PROJECT_ID SECRET_ID
```

### Private Git Repository — GitHub Fine-Grained PAT (Recommended)

GitHub recommends fine-grained PATs over SSH deploy keys. Use a `password` secret with HTTPS git auth:

```bash
# 1. Create a password secret with the GitHub PAT value
muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx

# 2. Bind to project — use x-access-token as the HTTPS username (GitHub convention)
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-git \
  --git-username x-access-token

# Optionally also inject as env var for runtime use:
# muveectl projects bind-secret PROJECT_ID --secret-id SECRET_ID \
#   --use-for-git --git-username x-access-token --env-var GITHUB_TOKEN

# 3. Deploy
muveectl projects deploy PROJECT_ID
```

The builder rewrites the git URL to `https://x-access-token:TOKEN@github.com/...` before cloning.

| Provider | `--git-username` |
|---|---|
| GitHub | `x-access-token` (default) |
| GitLab | `oauth2` |
| Bitbucket | your Bitbucket username |

### Private Build Dependencies (e.g. private Go modules)

If your Docker build needs secrets (for `go mod download`, private package registries, etc.), bind a secret with build flags:

```bash
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token
```

In Dockerfile:

```dockerfile
# syntax=docker/dockerfile:1.7
RUN --mount=type=secret,id=github_token \
    TOKEN="$(cat /run/secrets/github_token)" && \
    # ... use token for private deps ...
    go mod download
```

### Private Git Repository — SSH Deploy Key

For SSH-based authentication or providers that require it:

```bash
# 1. Generate key pair
ssh-keygen -t ed25519 -f deploy_key -N ""
# Add deploy_key.pub as a Deploy Key in your repository settings

# 2. Create SSH key secret
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file deploy_key

# 3. Bind to project
muveectl projects bind-secret PROJECT_ID --secret-id SECRET_ID --use-for-git

# 4. Deploy
muveectl projects deploy PROJECT_ID
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--server URL` | Override the configured server URL for this call |
| `--json` | Output raw JSON (pipe-friendly) |

## Git Repository Requirements

For a project to deploy successfully the repository must satisfy:

### Build
- Accessible via `git clone --depth=1` over HTTPS (public or with PAT via Secrets) or SSH (SSH key via Secrets)
- The configured branch must exist (default: `main`)
- A `Dockerfile` must exist at the configured path (default: `Dockerfile` in repo root). The `--dockerfile` flag takes a **file path** relative to the repo root (e.g. `web/Dockerfile`), not a directory path like `.` or `web/`.
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

## Adhoc Tunnel (self-hosted ngrok)

Publish a local port directly to the internet — no deployment, no Docker, no git repo required. The domain is deterministically generated from the current working directory and port number, so reconnecting from the same directory reuses the same URL.

```bash
# Publish local port 8080 to the internet
muveectl tunnel 8080
# → Tunnel active:
# →   https://t-bold-fox.example.com → 127.0.0.1:8080

# Override the auto-generated domain prefix
muveectl tunnel 3000 --domain t-my-demo

# Disable ForwardAuth (make the tunnel publicly accessible)
muveectl tunnel 8080 --no-auth
```

The tunnel stays open until you press Ctrl+C. All HTTP traffic to the generated `t-*.BASE_DOMAIN` URL is forwarded through a WebSocket connection to your local machine. By default, ForwardAuth is enabled — only authenticated users can access the tunnel. Pass `--no-auth` to make it publicly accessible.

**How the domain is generated:** SHA-256 of `cwd + port` selects an adjective-noun pair from a built-in word list, producing names like `t-bold-fox`, `t-calm-owl`, `t-keen-elk`. The same directory + port always produces the same name.

**Requirements:** The server must have `TUNNEL_BACKEND_URL` configured (set automatically in the default Docker Compose setup). There may be a ~5 second delay on first connection while Traefik picks up the new route.

## Typical Workflow

1. Get project IDs: `muveectl projects list --json`
2. Deploy a project: `muveectl projects deploy PROJECT_ID`
3. Check deployment status: `muveectl projects deployments PROJECT_ID`
4. Forward to local port: `muveectl projects port-forward PROJECT_ID --port 3000`
5. Publish a local dev server: `muveectl tunnel 8080`
