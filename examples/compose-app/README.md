# Compose example for muvee

Minimal multi-service docker-compose project that muvee can deploy as
a `project_type: compose` project. Demonstrates:

- Multiple services (`web`, `cache`, `data-init`)
- Pre-built images only (no `build:` directive — muvee rejects those)
- Named volumes that persist across redeploys (the project is pinned
  to a single deploy node so docker-local volumes survive)
- One service exposed via the muvee router (`web`, port 80)

## Deploy via muveectl

```sh
muveectl projects create \
  --name compose-demo \
  --compose \
  --git-url https://github.com/you/your-fork.git \
  --branch main \
  --compose-file docker-compose.yml \
  --expose-service web \
  --expose-port 80
muveectl projects deploy <project-id>
```

## Notes

- Volumes (`web_html`, `app_data`, `redis_data`) are local docker
  volumes on the pinned deploy node. They are preserved across
  redeploys and are only removed when the project itself is deleted.
- If the pinned deploy node goes offline, redeploys will refuse rather
  than migrate, because the data lives only on that node.
- All `image:` directives must reference pullable images. If you need
  a build step, use the regular muvee deployment project type instead.
