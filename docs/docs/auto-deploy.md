---
id: auto-deploy
title: Auto Deploy
sidebar_position: 6
---

# Auto Deploy

muvee can redeploy a project automatically when its source moves on. Two
independent triggers feed the same `Scheduler.TriggerDeployment` pipeline:

1. **Git trigger** — fires when the tracked branch advances. Hosted repos
   trigger immediately on `git push`; external repos are polled.
2. **Image-digest trigger** — `compose` projects only. Fires when the digest
   of any image referenced by `docker-compose.yml` changes upstream, even if
   the git tree has not moved (watchtower-style).

Both triggers respect a global kill switch and run as goroutines on the
control plane.

## Enabling it on a project

In the project detail page, flip the **Auto Deploy** toggle. The toggle is
hidden for tunnel projects and rejected for `domain_only` projects (they
have nothing to deploy).

The first time a project enables auto-deploy:

- Git trigger: `last_tracked_commit_sha` starts empty, so the next poll tick
  (or next push) deploys to align state. This is intentional.
- Image-digest trigger (compose only): the watcher's first tick *seeds* the
  recorded digests without triggering a deploy. Subsequent ticks compare and
  trigger when any digest moves.

## Global kill switch and intervals

Both triggers are gated by `auto_deploy_master_enabled`. Set it to `false`
from the Admin Settings page to pause everything without losing per-project
toggles.

| Setting | Default | Min | Notes |
|---|---|---|---|
| `auto_deploy_master_enabled` | `true` | — | Top-level kill switch |
| `auto_deploy_poll_interval_seconds` | `60` | `10` | Git poll cadence (external repos only) |
| `auto_deploy_image_watch_interval_seconds` | `600` | `60` | Image-digest poll cadence (compose only) |

Lower the image-watch interval cautiously — Docker Hub anonymous access is
rate-limited at ~200 requests per 6h per IP.

## Git trigger details

### Hosted repos (push-driven)

The git smart-HTTP handler invokes a post-receive callback after a successful
`git-receive-pack`. The server compares the new HEAD against
`last_tracked_commit_sha` and, on change, calls `TriggerDeployment` and
records the new SHA.

This path is event-driven: every push fires it. If you push 11 commits in
quick succession the project will see 11 redeploy attempts. Consider
squashing if redeploys are expensive.

### External repos (polled)

Every `auto_deploy_poll_interval_seconds`, the control plane runs
`git ls-remote` against each enabled external project and compares the
returned SHA with `last_tracked_commit_sha`. SSH and HTTPS auth mirror the
builder's behaviour:

- **HTTPS + password secret** — the URL is rewritten to
  `https://<git_username>:<token>@host/...` for the duration of one
  `ls-remote` call.
- **SSH key secret** — written to a tempfile and injected via
  `GIT_SSH_COMMAND="ssh -i <tempfile> -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null"`.

External polls are serial inside one tick. If one project's `ls-remote` is
slow or hangs, it caps at a 30s timeout and the next project starts.

## Image-digest trigger details

Compose deploys do not produce a built image — they `docker compose pull`
their declared images on the deploy node. So a redeploy can be useful even
when the git tree has not moved: when an image you depend on
(`redis:7-alpine`, `your-org/api:main`, etc.) has a new digest upstream.

Every `auto_deploy_image_watch_interval_seconds`, the watcher:

1. Lists `auto_deploy_enabled = TRUE` compose projects.
2. Fetches the project's `compose_file_path` at the tracked branch tip:
   - **Hosted** — `git show refs/heads/<branch>:<compose_file_path>` against
     the bare repo on disk.
   - **External** — shallow clones the repo to a tempdir, reads the file,
     and removes the tempdir.
3. Parses `services.*.image` strings from the YAML. Strings containing
   `${VAR}` or `$VAR` interpolation are skipped (logged at INFO). The
   compose escape `$$` is treated as the literal `$`, not a variable.
4. Resolves each image's manifest digest via the OCI registry HTTP API
   (`go-containerregistry/pkg/crane`). Auth is selected per-host:
   - The image's host matches `REGISTRY_ADDR` → authenticate with
     `REGISTRY_USER` / `REGISTRY_PASSWORD`.
   - Anything else → anonymous. This works for Docker Hub, ghcr.io public
     images, etc. Other private registries are not supported in the current
     release (the same limitation exists in compose deploy itself).
5. Compares with `last_tracked_image_digests` (a JSON map on the project
   row). If any image's digest moved, calls `TriggerDeployment` and writes
   the new map. New images that just appeared in the compose file (i.e. the
   user added a service) are recorded but do not trigger — that is the git
   trigger's job, since editing the compose file always lands as a commit.

Per-image digest lookups are bounded to 30s. A failed lookup retains the
previous digest so a transient registry hiccup will not cause a false
redeploy on the next tick.

## Registry connectivity requirement

The image-digest watcher needs the **control plane** itself to be able to
reach the registry. This is a small relaxation of the original guidance
that "the control plane never contacts the registry directly" — agents
still do all `docker push` / `docker pull` work, but the watcher now also
issues `HEAD /v2/<repo>/manifests/<tag>` from the control plane.

On startup the watcher probes `https://<REGISTRY_ADDR>/v2/`:

- HTTP `200` or `401` → registry is reachable, watcher starts.
- Anything else (DNS failure, refused connection, timeout, 5xx) → watcher
  logs a warning and exits cleanly. The rest of the server is unaffected;
  the git trigger keeps running.

If you split the control plane and the registry across networks where the
control plane cannot reach the registry, the image-digest trigger is
silently disabled. The git trigger still works.

## Compose-specific behaviour

`compose` projects are pinned to a single deploy node on first deploy so
their docker named volumes survive across redeploys. If that node goes
offline, auto-deploys (from either trigger) fail with
`refusing to migrate` and are logged but do not interrupt other projects'
ticks. Watch the project's deployment list for repeated failures.

`expose_service` and `expose_port` must be declared on the project before
auto-deploy will produce a valid task — `dispatchComposeDeploy` rejects
otherwise.

## What is *not* covered yet

- Other private registries (ghcr.io with PAT, ECR, Aliyun ACR, etc.). The
  image-digest watcher falls back to anonymous and will fail closed for
  any image those registries gate.
- Per-project rate limiting for the push trigger (a runaway 50-commit push
  will fire 50 redeploys; the poll path is naturally deduped).
- Parallel external polling (projects are checked serially per tick).

These are easy to add when the use cases show up.
