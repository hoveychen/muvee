---
id: secrets
title: Secrets & Environment Variables
sidebar_position: 4
---

# Secrets & Environment Variables

Muvee provides a built-in **Secrets** store for safely managing passwords, API tokens, and SSH private keys, encrypted at rest using AES-256-GCM. A personal secret does nothing on its own — it's a source you **copy** into a project's own **environment variables**, or into a project's **git credential** (used to clone a private repository). The one exception is the `registry` type, which is never copied and instead applies automatically to all of your compose projects.

## How It Works

```
User creates Secret → encrypted in DB (AES-256-GCM)
       ↓
User copies Secret into a Project's environment variable, or into a
Project's git credential — each copy is stored independently on the project
       ↓
On deploy / restart:
  • Runtime variables → injected as docker run -e KEY=VALUE
  • Build variables → passed to docker buildx as --secret id=KEY
  • Project's git credential (https_token / ssh_key) → builder uses it to clone
  • registry secrets → applied automatically to all of the owner's compose projects (no copy step)
```

Secrets are **write-only** — their values cannot be retrieved after creation. Because a copy is independent of its source, deleting or rotating the personal secret later does **not** change any project that already copied it.

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

| Type | Use case | Display |
|---|---|---|
| `password` | API tokens, database passwords, generic credentials | Write-only (value never shown) |
| `ssh_key` | PEM-format SSH private keys for cloning private git repositories | Write-only (value never shown) |
| `api_key` | API keys / provider tokens where a masked fingerprint helps identify which key is which | Shows first 4 + last 4 characters (e.g. `sk-1****wxyz`) |
| `env_var` | Non-sensitive configuration (public endpoints, feature flags) that benefits from central management | Shows full plaintext in the secrets list |
| `registry` | Pull credentials for a private container registry (e.g. `ghcr.io`) so compose projects can pull private images | Write-only (token never shown); registry address + username are shown |

:::warning
Only use `env_var` for values that are safe to view in the UI. Anything sensitive should use `password` or `api_key`.
:::

The type mostly matters for how the value defaults when it's copied into a project: copying an `env_var` secret produces a non-sensitive project variable, while every other type defaults to sensitive.

### Private registry credentials

A `registry` secret holds a login for a private container registry. Unlike other
secret types it is **not** copied per-project: every compose project you own
automatically uses **all** of your `registry` secrets when pulling images at
deploy time. This is how a `docker-compose` project pulls a private image such as
`ghcr.io/your-org/your-app:latest`.

The secret's **value** is the registry password / token; **registry address**
(e.g. `ghcr.io`) and **login username** are stored alongside it. At deploy time
the agent writes a temporary, per-deploy docker config with these credentials —
they never land in the shared agent docker config and never leak across tenants.

:::note
Only the **compose** deploy path uses registry credentials. Single-container
(Dockerfile/image) projects are built by Muvee and pulled from Muvee's own
registry, which is already authenticated.
:::

## Managing Secrets in the UI

Navigate to **Secrets** in the sidebar to:

- View all your secrets (names, types, and — for `api_key` / `env_var` — their preview)
- Create a new secret (one of the five types above)
- Delete a secret

## Project Environment Variables

Open a project and go to the **Environment Variables** tab to manage the
variables that belong to that project. Every project member and admin sees the
same list. Each row has a `KEY` (unique per project, must match
`^[A-Za-z_][A-Za-z0-9_]*$`), a value, and three switches:

- **Sensitive** (default on) — the value is write-only; the UI shows `Set · N chars` instead of the value. A sensitive variable cannot be turned back to non-sensitive — unset it and create it again instead.
- **Runtime** (default on) — injected into the running container's environment.
- **Build** (default off) — passed to `docker buildx` as a build secret whose id is the `KEY` (see [Private Build Dependencies](#private-build-dependencies-eg-private-go-modules) below).

Click **Copy from my secrets** to copy one of your personal secrets into the
project — the copy is independent, so later editing or deleting the personal
secret does not change the project.

:::note
Runtime variable changes take effect after a **restart** or **redeploy** of the project. Build variables apply on the **next deploy**.
:::

## Private Git Repository Credential

The credential Muvee uses to clone an **external** git repository is a
separate project setting, found under the project's **Config** tab in the
**Git repository** section — it is not one of the environment variables above.
Choose a type:

- **None** — clone anonymously (public repos only).
- **HTTPS token** — username (defaults to `x-access-token`) + a personal access token.
- **SSH deploy key** — an SSH private key.

The value is write-only; the section shows whether a credential is currently set. The new-project wizard writes this setting directly when you create a project pointing at a private repo.

## Managing Secrets via CLI

### Secrets

```bash
# List secrets (values never returned)
muveectl secrets list

# Create a password secret
muveectl secrets create --name GITHUB_TOKEN --type password --value ghp_xxxxx

# Create an SSH key from a file
muveectl secrets create --name DEPLOY_KEY --type ssh_key --value-file ~/.ssh/id_ed25519

# Create a private registry pull credential (applies to all your compose projects)
muveectl secrets create --name GHCR_PULL --type registry \
  --registry-addr ghcr.io --registry-username my-gh-user --value ghp_xxxxx

# Delete a secret
muveectl secrets delete SECRET_ID
```

### Project Environment Variables

```bash
# List a project's variables (KEY, SENSITIVE, RUNTIME, BUILD, VALUE_STATUS, ...)
muveectl projects env PROJECT_ID

# Create or update a variable (sensitive by default)
muveectl projects env set PROJECT_ID DATABASE_URL=postgres://...
muveectl projects env set PROJECT_ID LOG_LEVEL=debug --plain

# Build-time only (docker build secret, not injected at runtime)
muveectl projects env set PROJECT_ID NPM_TOKEN=npm_xxx --build --no-runtime

# Copy a personal secret (KEY defaults to the secret name; env_var secrets
# become plain variables, other types sensitive)
muveectl projects env copy PROJECT_ID --secret-id SECRET_ID [--key GITHUB_TOKEN] [--build] [--no-runtime]

# Delete a variable
muveectl projects env unset PROJECT_ID DATABASE_URL

# Print env vars effective inside the *running* container (secret-looking
# keys are masked; add --raw to see unmasked values)
muveectl projects env PROJECT_ID --live
muveectl projects env PROJECT_ID --live --raw
```

### Private Git Repository Credential

```bash
# Show the current credential (type, username, value_status)
muveectl projects git-credential PROJECT_ID

# HTTPS token (username defaults to x-access-token)
muveectl projects git-credential set PROJECT_ID --type https_token --value github_pat_xxxx

# SSH deploy key from a file
muveectl projects git-credential set PROJECT_ID --type ssh_key --value-file deploy_key

# Or copy one of your personal password / ssh_key secrets
muveectl projects git-credential set PROJECT_ID --from-secret-id SECRET_ID [--username oauth2]

# Remove it (clone anonymously)
muveectl projects git-credential clear PROJECT_ID
```

## Private Git Repository Workflows

Muvee supports two methods for cloning private repositories. Choose the one that fits your git provider.

---

### Method A: GitHub / GitLab Fine-Grained Access Token (HTTPS) — Recommended

GitHub now recommends **fine-grained personal access tokens** (PAT) over SSH deploy keys for repository access.

1. Generate a fine-grained PAT in **GitHub → Settings → Developer settings → Fine-grained tokens**.
   Grant it **Contents: Read-only** permission on the target repository.

2. Set it as the project's git credential:
   ```bash
   muveectl projects git-credential set PROJECT_ID --type https_token --value github_pat_xxxx
   ```
   The builder rewrites the git URL to `https://x-access-token:TOKEN@github.com/...` before cloning.

   | Provider | `--username` value |
   |---|---|
   | GitHub | `x-access-token` (default) |
   | GitLab | `oauth2` |
   | Bitbucket | your Bitbucket username |
   | Azure DevOps | `AzureDevOps` |

3. Trigger a deployment — no further configuration needed.

:::tip
You can also expose the same token to the app at runtime (e.g. to push images or interact with the GitHub API) as a project environment variable:
```bash
muveectl projects env set PROJECT_ID GITHUB_TOKEN=github_pat_xxxx
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
3. Set it as the project's git credential:
   ```bash
   muveectl projects git-credential set PROJECT_ID --type ssh_key --value-file deploy_key
   ```
4. Trigger a deployment — the builder uses the key via `GIT_SSH_COMMAND`.

## Security Notes

- Secret values are encrypted with **AES-256-GCM** before being stored in the database.
- Decrypted values are included in task payloads sent from the control plane to agent nodes over the internal network. Ensure this network is trusted.
- Secrets are scoped to the **user** who created them. Other users cannot see or use your secrets unless you share access. Project environment variables and git credentials, however, are visible to every member of that project.

## Private Build Dependencies (e.g. Private Go Modules)

If your Docker build needs secrets (for `go mod download`, private package registries, etc.), add a build variable — the build secret id is the variable's `KEY`:

```bash
# 1) Add a build-only variable (no runtime injection)
muveectl projects env set PROJECT_ID GITHUB_TOKEN=github_pat_xxxx --build --no-runtime

# 2) Deploy
muveectl projects deploy PROJECT_ID
```

In your Dockerfile:

```dockerfile
# syntax=docker/dockerfile:1.7
RUN --mount=type=secret,id=GITHUB_TOKEN \
    TOKEN="$(cat /run/secrets/GITHUB_TOKEN)" && \
    git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/" && \
    GOPRIVATE=github.com/your-org/* GONOSUMDB=github.com/your-org/* \
    go mod download
```

## Rotating a Secret

Rotating a **personal secret** (delete + recreate with a new value, or simply create a new one) does **not** update any project that already copied it — copies are independent. To roll a new value out to a project:

- Set it directly on the project variable: `muveectl projects env set PROJECT_ID KEY=NEW_VALUE`, or
- Run `projects env copy` again with the same `--key` to overwrite the project's variable with the secret's current value.

Either way, remember to `muveectl projects restart PROJECT_ID` (or redeploy) so a **runtime** variable change reaches the running container; **build** variables apply on the next deploy.
