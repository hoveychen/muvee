---
id: ai-agent
title: AI Agent Integration
sidebar_position: 5
---

# AI Agent Integration

This guide explains how to let a cloud-hosted AI agent — or any long-running automation — call `muveectl` (and the underlying Muvee API) on behalf of multiple end users, each with a distinct identity.

## The problem

`muveectl login` stores a single credential at `~/.config/muveectl/config.json`. That's fine for a personal laptop, but it falls apart when one process needs to act as different users:

- A shared AI agent serving multiple tenants cannot rewrite the config file between requests.
- Per-user OAuth flows require a browser and are unsuitable for headless workers.
- Project-scoped git tokens (`mvt_...`) only grant push access to one repo — they can't drive the rest of the API surface.

Personal Access Tokens (PATs) solve this cleanly: each end user mints a token tied to their account, hands it to the agent, and the agent injects it per-request.

## Personal Access Tokens (`mvp_...`)

A PAT authenticates as a specific user with that user's full permissions. You manage them in the web UI under **Settings → Access Tokens**, or from the CLI:

```bash
# List your PATs
muveectl tokens me list

# Create a new PAT (never expires)
muveectl tokens me create --name "claude-agent"

# Create a PAT that expires in 90 days (Go duration format: 2160h == 90d)
muveectl tokens me create --name "claude-agent" --expires 2160h

# Revoke a PAT
muveectl tokens me delete <token-id>
```

On creation the server returns the full token value **exactly once**. Store it immediately; it cannot be retrieved again.

Token prefixes at a glance:

| Prefix  | Scope              | Used for                                |
|---------|--------------------|-----------------------------------------|
| `mvt_`  | Single project     | `git push`, project-scoped CLI access   |
| `mvp_`  | Entire user account | AI agents, user-wide automation         |

## Injecting PATs into `muveectl`

`muveectl` resolves credentials with this precedence (highest wins):

1. `--server` / `--token` flags
2. `MUVEECTL_SERVER` / `MUVEECTL_TOKEN` environment variables
3. `~/.config/muveectl/config.json` (written by `muveectl login`)

An AI agent typically uses **2** or **1**, leaving the config file untouched so unrelated processes on the same host keep working.

### Pattern A — per-request env injection

```python
# Python agent dispatching commands for different end users
import os, subprocess

def run_muveectl(user_token: str, args: list[str]) -> str:
    env = os.environ.copy()
    env["MUVEECTL_SERVER"] = "https://muvee.example.com"
    env["MUVEECTL_TOKEN"]  = user_token  # user-supplied PAT
    return subprocess.check_output(["muveectl", *args], env=env, text=True)

# Alice deploys her project
run_muveectl(alice_pat, ["projects", "deploy", "proj_xxx"])

# Bob lists his datasets in the same process
run_muveectl(bob_pat, ["datasets", "list"])
```

The env vars scope to the child process only, so concurrent calls on behalf of different users cannot leak into each other.

### Pattern B — flag injection

Useful when you cannot set environment variables (e.g. inside a sandboxed tool executor):

```bash
muveectl --server https://muvee.example.com --token mvp_... projects list
```

### Pattern C — direct API calls

PATs work anywhere the session-based web UI works. Just send the token as a bearer credential:

```bash
curl -H "Authorization: Bearer $USER_PAT" \
  https://muvee.example.com/api/me
```

## Security model

- A PAT carries the **full permissions** of the user who minted it. Treat it like a password.
- Expired tokens (`expires_at <= now`) are rejected by the server. Choose an expiry when minting unless the agent is trusted long-term.
- Revoking a PAT takes effect immediately — every in-flight request that used it will receive `401` on its next call.
- Storage: the server only keeps a SHA-256 hash of each token. If you lose the plaintext, mint a new one.

## Lifecycle recommendations

1. **One PAT per user per agent.** If a user talks to two different agents, give each agent its own token — so revoking one doesn't break the other.
2. **Name PATs descriptively.** `"claude-agent-prod-2026-04"` tells you at a glance which tool to rotate.
3. **Rotate on schedule.** A 90-day expiry plus a refresh reminder in the agent's UI gives defense-in-depth against stale leaks.
4. **Revoke on offboarding.** When a user leaves the agent, call `DELETE /api/me/tokens/{id}` (or `muveectl tokens me delete`) before their session is closed.

## API reference

| Endpoint                      | Method | Auth             | Notes                                        |
|-------------------------------|--------|------------------|----------------------------------------------|
| `/api/me/tokens`              | GET    | session or PAT   | List the caller's PATs                       |
| `/api/me/tokens`              | POST   | session or PAT   | Body: `{"name": "...", "expires_in": "720h"}` (omit `expires_in` for never) |
| `/api/me/tokens/{id}`         | DELETE | session or PAT   | Revoke one PAT                               |
| `/api/me`                     | GET    | session or PAT   | Identity probe — confirm which user a PAT belongs to |
