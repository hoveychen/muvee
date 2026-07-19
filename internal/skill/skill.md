---
name: muveectl
version: 12
description: Operate the Muvee self-hosted PaaS via the muveectl CLI. Manages projects (create, update, deploy, delete, restart, pause / resume, port-forward, curl, exec / shell / cp into containers, describe, env, events, build/runtime logs), datasets (create, scan, delete, file ops), API tokens, and credential profiles for multi-environment switching (dev/staging/prod). Use when the user wants to interact with their Muvee server from the command line, trigger deployments, restart a container without a redeploy, inspect container state (describe / env / events), debug container crashes and restarts, run commands or open a shell inside a project container, copy files in/out of a container, hit auth-protected services from the terminal, manage infrastructure resources, switch between Muvee environments, manage dataset files (ls, pull, push, rm, mkdir, mv, cp), or self-update muveectl from the configured server.
---

# muveectl – Muvee CLI

## Installation

Fastest path (macOS/Linux) — one-liner that auto-detects OS/arch and installs from your muvee hub:

```bash
curl -fsSL YOUR_SERVER_URL/api/install.sh | sh
```

Or download the binary directly. The hub serves embedded binaries when available and transparently falls back to the matching GitHub release asset otherwise:

**macOS (Apple Silicon)**
```bash
curl -Lo muveectl YOUR_SERVER_URL/api/muveectl/muveectl_darwin_arm64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**macOS (Intel)**
```bash
curl -Lo muveectl YOUR_SERVER_URL/api/muveectl/muveectl_darwin_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**Linux (amd64)**
```bash
curl -Lo muveectl YOUR_SERVER_URL/api/muveectl/muveectl_linux_amd64
chmod +x muveectl && sudo mv muveectl /usr/local/bin/
```

**Windows (PowerShell)**
```powershell
Invoke-WebRequest -Uri YOUR_SERVER_URL/api/muveectl/muveectl_windows_amd64.exe -OutFile muveectl.exe
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

## Profiles (multiple environments)

Profiles let you keep credentials for several Muvee servers (e.g. `dev`, `staging`, `prod`) in one config file and switch between them kubectl-style. A legacy single-credential config is auto-migrated into a `default` profile on first read.

```bash
# Create a profile by logging in to its server (login both creates the
# profile and saves the OAuth token, then makes it the active profile)
muveectl login --server https://prod.example.com --profile prod

# List profiles (active one marked with *)
muveectl profile list

# Switch active profile
muveectl profile use prod

# Inspect (current active, or named)
muveectl profile current
muveectl profile show
muveectl profile show prod

# Remove a profile
muveectl profile rm dev
```

One-shot overrides (do not change the active profile):

```bash
# Per-command flag
muveectl --profile prod projects list

# Environment variable
MUVEECTL_PROFILE=prod muveectl projects list
```

Precedence for profile selection: `--profile` flag > `MUVEECTL_PROFILE` env > active profile in config. The existing `--server` / `--token` flags and `MUVEECTL_SERVER` / `MUVEECTL_TOKEN` env vars still override the per-profile credentials.

**IMPORTANT — resource IDs are scoped per server/profile.** Each profile points at a distinct Muvee server; project IDs, dataset IDs, and tokens exist **only** on the server they belong to. Running a `projects` / `datasets` command under the **wrong active profile** sends the request to a different server where that ID does not exist (or is not running) — you'll see `404 no running deployment`, "project not found", or `projects curl` returning unexpected content, which looks like the command "misbehaving" when in fact you hit the wrong environment.

When more than one profile exists, before acting either run `muveectl profile current` to confirm the active profile, or pass `--profile <name>` explicitly on each command:

```bash
muveectl --profile momoso projects curl <id> /api/health
```

Tip: `projects list` only lists the current profile's projects. If a project ID isn't in the `projects list` output, you're almost certainly pointed at the wrong profile — switch with `muveectl profile use <name>` (or add `--profile`) before retrying.

## Projects

```bash
muveectl projects list
muveectl projects create --name NAME --git-url URL \
  [--branch BRANCH] [--domain PREFIX] [--dockerfile PATH] \
  [--auth-required] [--auth-domains example.com,corp.com] \
  [--auth-bypass-paths "/health\n/api/public/*"] \
  [--description DESC] [--icon SVG_OR_URL] [--tags tag1,tag2]
muveectl projects create --name NAME --git-source hosted \
  [--branch BRANCH] [--domain PREFIX] [--dockerfile PATH] \
  [--description DESC] [--icon SVG_OR_URL] [--tags tag1,tag2]
muveectl projects create --name NAME --domain-only --domain PREFIX \
  [--description DESC] [--icon SVG_OR_URL] [--tags tag1,tag2]
muveectl projects create --name NAME --compose --git-url URL \
  --expose-service SERVICE --expose-port PORT \
  [--branch BRANCH] [--compose-file PATH] [--domain PREFIX] \
  [--description DESC] [--icon SVG_OR_URL] [--tags tag1,tag2]
muveectl projects create --name NAME --image-ref REF \
  [--container-port PORT] [--memory-limit LIMIT] [--volume-mount-path PATH] \
  [--domain PREFIX] [--auth-required] [--auth-domains example.com] \
  [--description DESC] [--icon SVG_OR_URL] [--tags tag1,tag2]
# --domain-only: reserves a tunnel domain prefix without a git repo or deployment.
#   Use with `muveectl tunnel <port> --project NAME` to route traffic to a local process.
#   The domain reservation persists even when no tunnel is connected (visitors see an offline page).
#   --domain is required; --git-url and --git-source are not allowed.
# --git-source hosted: creates a server-hosted bare git repo; returns a push URL
# --dockerfile PATH: path to the Dockerfile *file* relative to the repo root
#   default: "Dockerfile"  (repo root Dockerfile)
#   example: "web/Dockerfile" for a subdirectory
#   WRONG: "." or "web/" — must be a file path, not a directory
# --compose: deploys a docker-compose project (image: directives only, no `build:`)
#   Requires an external git repo (--git-source hosted is not supported).
#   Every service must reference a pre-built image; the agent runs `docker compose pull && up -d`.
#   --compose-file PATH (default docker-compose.yml) is the path *inside the repo*.
#   --expose-service / --expose-port pick which container port the muvee router publishes.
#   Compose projects are pinned to one deploy node so named volumes survive redeploys.
# --image-ref REF: deploys a single pre-built OCI image directly — no git repo, no build.
#   Examples: `ghcr.io/owner/repo:latest`, `docker.io/redis:7-alpine`, `myreg.example.com/svc:v1.2`.
#   Presence of --image-ref implicitly sets the project type to "image".
#   --container-port (default 8080) is the port the image listens on.
#   --volume-mount-path mounts a docker named volume at the given container path (persists across redeploys).
#   Auto-deploy watches the image digest and triggers a redeploy whenever the upstream tag is repushed.
#   Mutually exclusive with --git-url, --git-source, --compose, --domain-only.
muveectl projects get PROJECT_ID
muveectl projects update PROJECT_ID [--branch BRANCH] [--auth-required] [--no-auth] [--auth-domains DOMAINS] \
  [--auth-bypass-paths PATHS] [--description DESC] [--icon SVG_OR_URL] [--tags tag1,tag2] \
  [--auto-deploy] [--no-auto-deploy] \
  [--branding-site-name NAME] [--branding-logo-url URL] [--branding-favicon-url URL] \
  [--branding-primary-color HEX] [--branding-sidebar-bg HEX] [--branding-tagline TAG] \
  [--branding-description DESC] [--branding-footer-text TEXT] [--branding-trust-text "a,b,c"] \
  [--owner USERNAME_OR_UUID]
muveectl projects deploy PROJECT_ID
muveectl projects deployments PROJECT_ID
# Build/deploy phase logs (captured during `docker build` / `docker run`):
muveectl projects logs PROJECT_ID [--deployment DEPLOYMENT_ID]
# Runtime container stdout/stderr (debug crash / restart reasons):
muveectl projects runtime-logs PROJECT_ID [--tail 200] [--since 1h] [--follow]
muveectl projects metrics PROJECT_ID [--limit N]
muveectl projects port-forward PROJECT_ID [--port PORT]
muveectl projects curl PROJECT_ID [PATH] [-X METHOD] [-d BODY] [--data-stdin] \
  [-H 'Name: Value' ...] [-i]
muveectl projects delete PROJECT_ID
```

### Authentication (`--auth-required`)

When enabled, Traefik intercepts every request and redirects unauthenticated users to the configured OAuth provider (Google, Feishu, WeCom, DingTalk, etc.) before forwarding to the container.

- `--auth-required` — enable protection
- `--no-auth` — disable protection
- `--auth-domains example.com,corp.com` — restrict to specific email domains (optional; omit to allow all authenticated users)
- `--auth-bypass-paths "/health\n/api/public/*"` — newline-separated paths that skip authentication. Use `*` suffix for prefix matching (e.g. `/api/public/*` matches all paths under `/api/public/`). Exact paths (e.g. `/health`) match only that path.

The authenticated user's identity is forwarded to the container via HTTP headers:

| Header | Value |
|--------|-------|
| `X-Forwarded-User` | Email address (e.g. `alice@example.com`) |
| `X-Forwarded-User-Name` | Display name (when available from OAuth provider) |
| `X-Forwarded-User-Avatar` | Avatar URL (when available from OAuth provider) |
| `X-Forwarded-User-Provider` | OAuth provider name (e.g. `google`, `feishu`) |

```python
# Python / Flask example
email  = request.headers.get("X-Forwarded-User")
name   = request.headers.get("X-Forwarded-User-Name")
avatar = request.headers.get("X-Forwarded-User-Avatar")
```

```go
// Go example
email  := r.Header.Get("X-Forwarded-User")
name   := r.Header.Get("X-Forwarded-User-Name")
avatar := r.Header.Get("X-Forwarded-User-Avatar")
```

```typescript
// Node.js / Express example
const email  = req.headers["x-forwarded-user"]
const name   = req.headers["x-forwarded-user-name"]
const avatar = req.headers["x-forwarded-user-avatar"]
```

For the full integration guide (frontend userinfo API, logout, CLI/headless Device Flow), see the [Service Auth Integration](https://hoveychen.github.io/muvee/docs/service-auth-integration) documentation.

### Project Metadata (`--description`, `--icon`, `--tags`)

Projects support display metadata shown in the community feed and project detail pages:

- `--description "Short project description"` — a brief summary of what the project does
- `--icon '<svg>...</svg>'` — project icon; **recommended to use inline SVG** for crisp rendering at any size. Keep the SVG simple and small (single-color, 24x24 or 32x32 viewBox). You can also pass a URL to an external image.
- `--tags "tool,ai,demo"` — comma-separated tags for categorization and discovery

All three flags work on both `projects create` and `projects update`.

```bash
# Set metadata on creation
muveectl projects create --name my-app --git-url https://github.com/me/app \
  --description "A real-time dashboard for sensor data" \
  --icon '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12h4l3-9 4 18 3-9h4"/></svg>' \
  --tags "dashboard,iot,realtime"

# Update metadata later
muveectl projects update PROJECT_ID \
  --description "Updated description" \
  --tags "dashboard,iot,realtime,v2"
```

**Icon tips:**
- Draw a simple SVG icon that represents the project's purpose (e.g. a chart for analytics, a robot for AI, a globe for web apps)
- Use `viewBox="0 0 24 24"` and `stroke="currentColor"` so it adapts to light/dark themes
- Keep the SVG under ~500 characters for readability in CLI output

### Auto-Deploy (`--auto-deploy`)

Redeploys the project automatically when the tracked source changes. Works in two modes depending on project type:

- **Git projects (hosted or external)** — server polls the tracked branch and redeploys on a new commit. Hosted repos trigger immediately on push.
- **Image / compose projects** — server polls the upstream image digest and redeploys when the tag is repushed.

```bash
muveectl projects update PROJECT_ID --auto-deploy      # enable
muveectl projects update PROJECT_ID --no-auto-deploy   # disable
```

Not supported for `--domain-only` (tunnel) projects.

### Sign-in Page Branding (`--branding-*`)

When a project is auth-protected, Traefik's ForwardAuth renders a login page on the project's subdomain. The branding flags customise that page — what the **downstream users** of this project see, distinct from platform-wide branding.

| Flag | What it controls |
|---|---|
| `--branding-site-name "Acme Docs"` | Headline shown on the sidebar and browser tab |
| `--branding-logo-url https://...` | Logo image; replaces the site name on sidebar/header |
| `--branding-favicon-url https://...` | `.ico` / `.png` / `.svg` shown in the browser tab |
| `--branding-primary-color "#4f46e5"` | Hex colour tinting the provider-button hover state |
| `--branding-sidebar-bg "#1a0033"` | Hex colour for the desktop sidebar background |
| `--branding-tagline "Acme Inc."` | Small uppercase tagline above the brand |
| `--branding-description "Internal tools..."` | Multi-line description shown under the brand |
| `--branding-footer-text "© Acme 2026"` | Single-line footer (empty hides the row entirely) |
| `--branding-trust-text "Encrypted,SOC 2,GDPR"` | Comma-separated trust badges (up to 3) below the sign-in buttons |

Empty fields fall back to the platform-wide branding configured in admin settings.

```bash
muveectl projects update my-app \
  --branding-site-name "Acme Docs" \
  --branding-primary-color "#4f46e5" \
  --branding-sidebar-bg "#1a0033" \
  --branding-trust-text "Encrypted,SOC 2,GDPR ready"
```

### Reassigning Project Ownership (`--owner`)

Admin only. Hands a project to a different platform user. Accepts a username or UUID:

```bash
muveectl projects update my-app --owner alice
muveectl projects update my-app --owner 8c7b6a5d-...
```

The owner change goes through a dedicated admin endpoint (`PUT /api/projects/:id/owner`) — non-admin callers get 403. You can combine `--owner` with other update flags in the same command; the normal update is applied first, then ownership is reassigned.

### Logs (build vs. runtime)

There are two log surfaces and they capture different things — picking the wrong one is the #1 reason "I can't see why my container died" debugging stalls.

```bash
# Build/deploy phase logs — what the agent printed while running
#   `docker build` and `docker run` for a specific deployment.
# Use this for: build failures, push failures, image pull errors, port-bind errors.
muveectl projects logs PROJECT_ID
muveectl projects logs PROJECT_ID --deployment DEPLOYMENT_ID   # any past deployment
muveectl projects deployments PROJECT_ID                       # list deployment IDs

# Runtime container stdout/stderr — `docker logs muvee-<domain_prefix>` on
# the deploy node. Captures what your application itself printed (panics,
# request errors, restart reasons, OOM stack traces).
# Use this for: crash debugging, restart-loop investigation, "why is my app silent".
muveectl projects runtime-logs PROJECT_ID                      # last 200 lines
muveectl projects runtime-logs PROJECT_ID --tail 1000 --since 30m
muveectl projects runtime-logs PROJECT_ID --follow             # poll every 5 s, Ctrl-C to stop
```

Limits and caveats:
- `runtime-logs` requires the project to have a running deployment (returns 409 otherwise — if the container has been removed entirely, use `projects logs` to see why the deploy failed).
- Output is capped at 1 MiB per snapshot server-side; pair `--tail` and `--since` to narrow the window.
- `--follow` is a 5-second poll loop, not a true stream — small overlap/gap risk at the boundary. Re-run without `--follow` for full fidelity on a fixed window.

### Container Metrics

```bash
# Show the latest resource sample and historical table
muveectl projects metrics PROJECT_ID

# Fetch more history (default: 60 samples)
muveectl projects metrics PROJECT_ID --limit 120
```

Fields returned per sample: `collected_at`, `cpu_percent`, `mem_usage_bytes`, `mem_limit_bytes`, `net_rx_bytes`, `net_tx_bytes`, `block_read_bytes`, `block_write_bytes`.

### Workspace

The workspace at `/workspace` is the **only** persistent storage attached to the running container — anything written elsewhere is lost on restart or redeploy. Note that even `/workspace` only persists once the project's `volume_mount_path` is set to `/workspace`; it is **not** attached automatically. See "Persistence (CRITICAL)" under "Git Repository Requirements" → "Runtime" before writing application code that handles user data, uploads, databases, or any state that must survive a redeploy.

Use these commands to inspect and transfer files without redeploying.

```bash
# List files in the workspace root (or a subdirectory)
muveectl projects workspace ls PROJECT_ID
muveectl projects workspace ls PROJECT_ID some/subdir

# Download a file from the workspace
muveectl projects workspace pull PROJECT_ID remote/path/file.txt
muveectl projects workspace pull PROJECT_ID remote/path/file.txt local_copy.txt

# Upload a local file to the workspace
muveectl projects workspace push PROJECT_ID local_file.bin
muveectl projects workspace push PROJECT_ID local_file.bin --remote-path uploads/file.bin

# Delete a file from the workspace
muveectl projects workspace rm PROJECT_ID remote/path/file.txt
```

### Inspect & Kick (`restart` / `env` / `describe` / `events`)

Four kubectl-style read-only / single-shot commands for poking at a running project without opening a shell. All dispatch a one-shot agent task (5 s typical end-to-end latency) and stream back the result.

```bash
# Restart the running container — no rebuild, no redeploy. Useful after
# changing secrets or env vars, or to clear memory.
muveectl projects restart PROJECT_ID

# Print env vars effective inside the container. Secret-looking keys
# (PASSWORD/SECRET/TOKEN/KEY/CREDENTIAL/...) mask their values as '***';
# add --raw to see the unmasked value.
muveectl projects env PROJECT_ID
muveectl projects env PROJECT_ID --raw

# Kubectl-describe-style snapshot: status, exit code, OOMKilled, restart
# count, image+sha, ports, mounts, env keys.
muveectl projects describe PROJECT_ID
muveectl projects describe PROJECT_ID --output json   # raw JSON for scripts

# Tail platform events recorded server-side (deploy.started, deploy.completed,
# deploy.failed, restart, pause, resume, container.oom_killed). In-memory ring
# buffer capped at 200 events per project — lost on server restart.
muveectl projects events PROJECT_ID
muveectl projects events PROJECT_ID --follow         # poll every 3 s

# Soft-pause: 'docker stop' the container(s) — CPU/memory freed, image and
# data kept. While paused every deploy path is blocked. Resume is instant.
muveectl projects pause PROJECT_ID
muveectl projects resume PROJECT_ID                   # 'docker start' — no rebuild
```

When to reach for each:
- **`restart`** when env vars / secrets changed but the project file is unchanged, or to break a wedged process without a deploy cycle.
- **`pause` / `resume`** to park an idle project: pause frees its CPU/memory (image and volumes are kept, config preserved) and blocks all redeploys; resume brings it back with no rebuild. Use instead of `delete` when you want the project back later. Note: paused frees compute, not disk — use `delete` if you need the image layers reclaimed.
- **`describe`** as the first stop when "why did my container die?" — it gives you ExitCode, OOMKilled, RestartCount, and Health on one screen.
- **`env`** to confirm an injected secret or auto-injected `MUVEE_*` env var actually reached the container.
- **`events`** to follow what the platform itself thinks is happening — useful when you suspect the deploy lifecycle, not the app, is misbehaving.

### Interactive Debugging (`exec` / `shell` / `cp`)

For inspecting a running container directly — running ad-hoc commands, opening a shell, or copying files in/out — there are three kubectl-style subcommands that route through the deploy agent's outbound control channel:

```bash
# Run a one-off command inside the project container (PTY-backed, like kubectl exec).
muveectl projects exec PROJECT_ID -- ls -la /app
muveectl projects exec PROJECT_ID -- sh -c 'env | grep API'

# Open an interactive shell — convenience for 'projects exec ID -- /bin/sh'.
# Most muvee images are Alpine / distroless, so /bin/sh is the safe default.
muveectl projects shell PROJECT_ID

# Copy files between the local filesystem and the container, in either direction.
# Exactly one side must be a PROJECT:PATH reference.
muveectl projects cp ./config.json PROJECT_ID:/app/config.json   # upload
muveectl projects cp PROJECT_ID:/app/logs ./logs-dump            # download
```

When to use which:
- **`exec`** — automated one-shot checks ("does this env var resolve?", "ls /tmp"). Exit code propagates back so you can chain it in shell scripts.
- **`shell`** — open-ended debugging from a real terminal. Ctrl-C / SIGWINCH / colorized output all work because the agent allocates a host PTY around `docker exec -ti`.
- **`cp`** — pulling crash dumps, log files, or coredumps out; pushing fixed config or a one-off script in. The wire is a tar stream so directories and (read-only) symlinks survive.

Caveats:
- These all require a **running deployment**. With no live container the server returns 404 ("no running deployment") — fall back to `projects logs` to see why the deploy failed.
- The deploy node's agent must be connected to the control plane (logged as `agentcontrol: connected to ...`). A disconnected agent surfaces as 503 ("agent for node ... not currently connected").
- Project access is gated by owner / member / admin — same rule as `projects curl` and `port-forward`.

## Self-Update (`muveectl upgrade`)

Refreshes the muveectl binary in place from the **current profile's** server, so your CLI always matches whichever environment you're pointed at.

```bash
muveectl upgrade
```

How it works: downloads `<server>/api/muveectl/muveectl_<os>_<arch>` to a temp file in the same directory as the running binary, chmods it executable, and atomically renames it over the live binary. The server transparently serves the embedded binary or 302-redirects to the matching GitHub release asset.

When to use:
- After a Muvee server upgrade, to pull the matching CLI version (`muveectl version` reports both Client and Server versions, so you'll see the drift).
- When switching profiles between environments at different versions: `muveectl profile use prod && muveectl upgrade`.
- In place of re-running the install one-liner for routine updates.

If the install directory isn't writable by the current user (e.g. `/usr/local/bin` without admin), the command fails with `need write access to upgrade in place` — re-run with `sudo`, or `chown` the binary first.

## Reaching Auth-Protected Projects from the CLI

Projects with `--auth-required` (or `access_mode=private`) are gated by Traefik ForwardAuth in the browser, but `muveectl` ships two ways to bypass that gate using your CLI identity. Both rely on the server-side proxy at `/api/projects/{id}/proxy/*`, which:

- Authenticates the request with your CLI Bearer token.
- Authorizes against project membership (owner / member / admin) — the per-project `project_access_users` ACL is **not** consulted here.
- Connects directly to the deployment's host:port, fully bypassing Traefik / forward-auth.
- Injects `X-Forwarded-User: <your email>` into the request before it hits the container, so the container sees the same identity it would through the browser.

Pick the form that fits the use case.

### `projects curl` — one-off requests

Best for scripts, health checks, quick API pokes. Single request, no local listener.

> **Multiple profiles?** `PROJECT_ID` is resolved against the **active profile's** server. If curl returns `404 no running deployment` or unexpected content for an ID you know is deployed, you're likely on the wrong profile — confirm with `muveectl profile current` or pass `--profile <name>` (e.g. `muveectl --profile momoso projects curl <id> /`). See "Profiles" above.

```bash
# GET the homepage
muveectl projects curl PROJECT_ID /

# Show response status + headers, then body
muveectl projects curl PROJECT_ID /api/me -i

# POST JSON
muveectl projects curl PROJECT_ID /api/users \
  -X POST \
  -H 'Content-Type: application/json' \
  -d '{"name":"alice"}'

# Stream a file as the request body
cat payload.bin | muveectl projects curl PROJECT_ID /api/upload \
  -X POST --data-stdin -i
```

Flags:
- `-X, --method` — HTTP method (default `GET`).
- `-d, --data` — inline string body.
- `--data-stdin` — read body from stdin (overrides `-d`).
- `-H, --header` — extra header in `Name: Value` form; repeatable.
- `-i, --include` — print response status line + headers before the body.

Exit code is `1` on any HTTP `>= 400` so shell scripts can check `$?`.

### `projects port-forward` — sustained sessions

Best for development — opens a local TCP listener that proxies every request through the same authenticated path. Reuse from a browser, a frontend dev server, or repeated curls.

```bash
# Auto-pick a free local port
muveectl projects port-forward PROJECT_ID

# Use a specific local port
muveectl projects port-forward PROJECT_ID --port 3000
```

Then talk to it as if it were running locally:

```bash
curl http://127.0.0.1:3000/api/some-endpoint
```

Useful when your code needs to call APIs exposed by a deployed project without dealing with OAuth login flows or TLS certificates.

## Datasets

```bash
muveectl datasets list
muveectl datasets create --name NAME --nfs-path NFS_PATH
muveectl datasets get DATASET_ID
muveectl datasets scan DATASET_ID
muveectl datasets delete DATASET_ID
```

### Dataset File Operations

Manage files inside a dataset's NFS directory — works like an object storage client.

```bash
# List files in the dataset root (or a subdirectory)
muveectl datasets ls DATASET_ID
muveectl datasets ls DATASET_ID some/subdir

# Download a file from the dataset
muveectl datasets pull DATASET_ID remote/path/file.txt
muveectl datasets pull DATASET_ID remote/path/file.txt local_copy.txt

# Upload a local file to the dataset
muveectl datasets push DATASET_ID local_file.bin
muveectl datasets push DATASET_ID local_file.bin --remote-path uploads/file.bin

# Delete a file or directory from the dataset
muveectl datasets rm DATASET_ID remote/path/file.txt

# Create a directory
muveectl datasets mkdir DATASET_ID new/subdir

# Move or rename a file/directory
muveectl datasets mv DATASET_ID old/path new/path

# Copy a file within the dataset
muveectl datasets cp DATASET_ID source/file.txt dest/file.txt
```

## API Tokens

```bash
muveectl tokens list PROJECT_ID
muveectl tokens create PROJECT_ID [--name NAME]   # token value shown once on creation
muveectl tokens delete PROJECT_ID TOKEN_ID
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

# Create a private registry pull credential (type=registry).
# Applies to ALL your compose projects automatically — no per-project bind needed.
# The agent uses it to pull private compose images (e.g. ghcr.io) at deploy time.
muveectl secrets create --name GHCR_PULL --type registry \
  --registry-addr ghcr.io --registry-username my-gh-user --value ghp_xxxxx

# Delete a secret
muveectl secrets delete SECRET_ID
```

### Binding Secrets to Projects

Most secret types must be bound to a project; `registry` secrets are the
exception — they apply to all of your compose projects automatically. Bindable
secrets can be used in three ways:
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

### Persistence (CRITICAL — read before writing app code)

The container filesystem is **ephemeral**. Any data written outside `/workspace` — including `/tmp`, `/app`, the working directory, or any custom path — is **lost on restart, redeploy, scale, or host migration**. Docker volumes, bind mounts, and host paths declared in the Dockerfile are **not** persisted by Muvee.

**The only persistent writable path inside a Muvee container is `/workspace`.** When deploying any project that needs to keep state across runs (user uploads, generated artifacts, SQLite/DuckDB files, model checkpoints, vector indexes, cache, logs you want to keep), the destination path **must** be under `/workspace`.

**But `/workspace` is NOT mounted automatically — you must enable it.** A `deployment`-type project only gets a persistent `/workspace` bind mount when its `volume_mount_path` field is set to `/workspace`. If that field is empty (the default for a freshly created project), `/workspace` is just an ordinary directory in the ephemeral container layer, and **everything written under it is lost on every restart / redeploy** — the most insidious form of this bug, because the write path looks correct (`DATA_DIR=/workspace/data` in the Dockerfile) while nothing is actually persisted. Enable it once:

```bash
muveectl projects update PROJECT_ID --volume-mount-path /workspace
```

The mount is applied at container creation, so after setting the field you must **redeploy** (`muveectl projects deploy PROJECT_ID`) — a plain `restart` reuses the existing mount-less container and won't pick it up. Then verify: `muveectl projects describe PROJECT_ID` must list a bind mount at `/workspace` under `Mounts` (and `df /workspace` inside the container must show a real device, not `overlay`). An empty `Mounts` array means the field didn't take and your data is still ephemeral.

When wiring up a project — whether you're writing the app code, the Dockerfile, or a config file — actively configure persistence:
- Point the app's data directory at a subdirectory of `/workspace` (e.g. `/workspace/data`, `/workspace/uploads`, `/workspace/db`). Use the framework's standard env var (`DATA_DIR`, `STORAGE_PATH`, `SQLITE_PATH`, etc.) or hardcode the path if no knob exists.
- For SQLite, set the DB file to `/workspace/<name>.db`. For DuckDB / LiteFS, similarly under `/workspace/`.
- For user uploads, write to `/workspace/uploads/` and serve from there.
- For caches you want to survive restarts (model weights, embeddings), use `/workspace/cache/`.
- If the framework defaults to writing under `~`, `./data`, or `/var/lib/...`, **override it** to point under `/workspace` — those defaults will silently lose data.
- **Set `volume_mount_path=/workspace` on the project itself** (see the caveat above). Pointing `DATA_DIR` at `/workspace/data` in the Dockerfile is only half the job — without the project field, `/workspace` isn't backed by a persistent volume and the Dockerfile setting is moot.

**Do not** assume "I'll just put it in `./data` and it'll be fine" — that path is inside the ephemeral container layer and disappears on the next deploy. If a user reports data loss after redeploy, the cause is almost always a write path that wasn't under `/workspace`.

(Read-only dataset mounts at `/data/<dataset_name>` are separate; see "Dataset mounts" below — those are sourced from NFS and not user-writable for persistence.)

**Compose and image projects use a different persistence model.** The `/workspace` rule applies to `deployment`-type projects — but only once `volume_mount_path` is set to `/workspace` on the project (see the caveat above; the mount is **not** provisioned automatically). For:
- `--compose` projects: persistence is whatever the docker-compose.yml declares — named volumes, bind mounts, etc. Compose projects are pinned to one deploy node so docker-local volumes survive redeploys.
- `--image-ref` projects: pass `--volume-mount-path /your/path` to mount a docker named volume at that container path. The volume name is fixed (`app-data`) and survives across redeploys on the pinned deploy node. Without `--volume-mount-path`, the container has no persistent storage.

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

# Use a project-scoped tunnel (domain_only project, persistent domain)
muveectl tunnel 8080 --project my-api

# Disable ForwardAuth (make the tunnel publicly accessible)
muveectl tunnel 8080 --no-auth
```

The tunnel stays open until you press Ctrl+C. All HTTP traffic to the generated `t-*.BASE_DOMAIN` URL is forwarded through a WebSocket connection to your local machine. By default, ForwardAuth is enabled — only authenticated users can access the tunnel. Pass `--no-auth` to make it publicly accessible.

**Ephemeral vs project-scoped tunnels:**
- `--domain t-*` (default): ephemeral domain, goes away when the tunnel disconnects.
- `--project <name>`: uses the `domain_prefix` of a `domain_only` project. The domain reservation persists even when no tunnel is connected (visitors see a friendly offline page). Traffic is logged in the project's traffic panel on the hub dashboard.

**How the domain is generated:** SHA-256 of `cwd + port` selects an adjective-noun pair from a built-in word list, producing names like `t-bold-fox`, `t-calm-owl`, `t-keen-elk`. The same directory + port always produces the same name.

**Requirements:** The server must have `TUNNEL_BACKEND_URL` configured (set automatically in the default Docker Compose setup). There may be a ~5 second delay on first connection while Traefik picks up the new route.

## Typical Workflow

1. Get project IDs: `muveectl projects list --json`
2. Deploy a project: `muveectl projects deploy PROJECT_ID`
3. Check deployment status: `muveectl projects deployments PROJECT_ID`
4. Forward to local port: `muveectl projects port-forward PROJECT_ID --port 3000`
5. One-off auth-protected request: `muveectl projects curl PROJECT_ID /api/health`
6. Publish a local dev server: `muveectl tunnel 8080`
7. Use a project-bound tunnel: `muveectl tunnel 8080 --project my-api`
