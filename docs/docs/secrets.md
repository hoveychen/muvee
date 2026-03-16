---
id: secrets
title: Secrets Management
sidebar_position: 4
---

# Secrets Management

Muvee provides a built-in Secrets store for safely managing passwords, API tokens, and SSH private keys. Secrets are encrypted at rest using AES-256-GCM and are injected into deployments at runtime.

## How It Works

```
User creates Secret → encrypted in DB (AES-256-GCM)
       ↓
User binds Secret to Project (env_var_name, use_for_git, use_for_build, build_secret_id)
       ↓
On deploy, scheduler decrypts secrets and:
  • SSH key (use_for_git=true) → builder uses for git clone
  • Any secret with use_for_build=true + build_secret_id → builder passes docker buildx --secret id=build_secret_id
  • Any secret with env_var_name → injected as docker run -e KEY=VALUE
```

Secrets are **write-only** — their values cannot be retrieved after creation. To rotate a secret, delete the old one and create a new one with the same name, then re-bind it to your projects.

## Prerequisites

Set the `SECRET_ENCRYPTION_KEY` environment variable on the control plane before creating any secrets. This must be a **64-character hex string** (32 bytes):

```bash
# Generate a secure key
openssl rand -hex 32
# e.g. a3f4e1b2c8d7...

# Set it in your environment / .env file
SECRET_ENCRYPTION_KEY=a3f4e1b2c8d7...
```

:::caution
If `SECRET_ENCRYPTION_KEY` is not set, secret creation will be disabled. Back up this key — losing it makes all encrypted secrets unrecoverable.
:::

## Secret Types

| Type | Use case |
|---|---|
| `password` | API tokens, database passwords, generic credentials |
| `ssh_key` | PEM-format SSH private keys for cloning private git repositories |

## Managing Secrets in the UI

Navigate to **Secrets** in the sidebar to:

- View all your secrets (names and types only — values are never shown)
- Create a new secret (password or SSH key)
- Delete a secret

## Binding Secrets to a Project

Open a project and click the **Secrets** tab to:

- Attach / detach secrets from the project
- Set the **environment variable name** each secret is injected as (e.g. `GITHUB_TOKEN`, `DATABASE_PASSWORD`)
- For SSH key secrets, enable **"Use for git clone"** — this makes the builder use the key when cloning the git repository
- Enable **"Use for docker build secret"** and set **Build Secret ID** (e.g. `github_token`) so Dockerfile can read `/run/secrets/github_token` during build

:::note
Environment variable injection takes effect on the **next deployment**. Redeploy the project after updating secret bindings.
:::

## Managing Secrets via CLI

### Secrets

```bash
# List secrets (values never returned)
muveectl secrets list

# Create a password secret
muveectl secrets create --name GITHUB_TOKEN --type password --value ghp_xxxxx

# Create an SSH key from a file
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# Delete a secret
muveectl secrets delete SECRET_ID
```

### Project Bindings

```bash
# List secrets bound to a project
muveectl projects secrets PROJECT_ID

# Bind a secret as an environment variable
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --env-var GITHUB_TOKEN

# Bind an SSH key secret for git clone
muveectl projects bind-secret PROJECT_ID \
  --secret-id SSH_KEY_SECRET_ID \
  --use-for-git

# Bind a secret for docker buildx secret mount
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# Remove a secret binding
muveectl projects unbind-secret PROJECT_ID SECRET_ID
```

## Private Git Repository Workflows

Muvee supports two methods for cloning private repositories. Choose the one that fits your git provider.

---

### Method A: GitHub / GitLab Fine-Grained Access Token (HTTPS) — Recommended

GitHub now recommends **fine-grained personal access tokens** (PAT) over SSH deploy keys for repository access.

1. Generate a fine-grained PAT in **GitHub → Settings → Developer settings → Fine-grained tokens**.
   Grant it **Contents: Read-only** permission on the target repository.

2. Create a `password` secret in Muvee with the token value:
   ```bash
   muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx
   ```

3. Bind the secret to your project, enabling HTTPS git auth:
   ```bash
   muveectl projects bind-secret PROJECT_ID \
     --secret-id SECRET_ID \
     --use-for-git \
     --git-username x-access-token
   ```
   The builder rewrites the git URL to `https://x-access-token:TOKEN@github.com/...` before cloning.

   | Provider | `--git-username` value |
   |---|---|
   | GitHub | `x-access-token` (default) |
   | GitLab | `oauth2` |
   | Bitbucket | your Bitbucket username |
   | Azure DevOps | `AzureDevOps` |

4. Trigger a deployment — no further configuration needed.

:::tip
You can also inject the same token as an environment variable for use at runtime (e.g. to push images or interact with the GitHub API):
```bash
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --env-var GITHUB_TOKEN \
  --use-for-git \
  --git-username x-access-token
```
:::

---

### Method B: SSH Deploy Key

Use this method when your provider requires SSH, or when you prefer key-based authentication.

1. Generate an SSH key pair:
   ```bash
   ssh-keygen -t ed25519 -f deploy_key -N ""
   ```
2. Add `deploy_key.pub` as a **Deploy Key** in your repository settings (GitHub: _Settings → Deploy keys_).
3. Create an SSH key secret in Muvee:
   ```bash
   muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file deploy_key
   ```
4. Bind it to the project with git clone enabled:
   ```bash
   muveectl projects bind-secret PROJECT_ID \
     --secret-id SECRET_ID \
     --use-for-git
   ```
5. Trigger a deployment — the builder uses the key via `GIT_SSH_COMMAND`.

## Security Notes

- Secret values are encrypted with **AES-256-GCM** before being stored in the database.
- Decrypted values are included in task payloads sent from the control plane to agent nodes over the internal network. Ensure this network is trusted.
- Secrets are scoped to the **user** who created them. Other users cannot see or use your secrets unless you share access.

## Build-Time Secret Example (Private Go Modules)

When your repository needs private Go modules, you can bind a PAT secret for build-time use:

```bash
# 1) Create PAT secret
muveectl secrets create --name GITHUB_TOKEN --type password --value github_pat_xxxx

# 2) Bind for docker build secret
muveectl projects bind-secret PROJECT_ID \
  --secret-id SECRET_ID \
  --use-for-build \
  --build-secret-id github_token

# 3) Deploy
muveectl projects deploy PROJECT_ID
```

In your Dockerfile:

```dockerfile
# syntax=docker/dockerfile:1.7
RUN --mount=type=secret,id=github_token \
    TOKEN="$(cat /run/secrets/github_token)" && \
    git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/" && \
    GOPRIVATE=github.com/your-org/* GONOSUMDB=github.com/your-org/* \
    go mod download
```
