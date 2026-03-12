# Contributing to muvee

Thank you for your interest in contributing!

## Development Setup

```bash
git clone https://github.com/hoveychen/muvee.git
cd muvee

# Install web dependencies
make web-install

# Build everything (frontend + Go binaries)
make build

# Run the server locally (requires PostgreSQL)
export DATABASE_URL="postgres://muvee:muvee@localhost:5432/muvee?sslmode=disable"
export GOOGLE_CLIENT_ID=...
export GOOGLE_CLIENT_SECRET=...
make run

# Run the web dev server with hot reload (proxies API to :8080)
make web-dev
```

## Project Structure

```
cmd/
  server/       — Control plane entrypoint
  agent/        — Node agent (builder + deploy)
  authservice/  — ForwardAuth sidecar

internal/
  api/          — HTTP handlers
  auth/         — Google OIDC, JWT, RBAC
  builder/      — git clone + docker build
  datacache/    — LRU dataset cache
  deployer/     — docker run + Traefik labels
  monitor/      — NFS file change tracking
  scheduler/    — affinity scoring + task dispatch
  store/        — PostgreSQL access layer

web/            — React + Vite frontend
  src/
  embed/        — Go embed package (holds compiled dist/)

docs/           — Docusaurus documentation site
db/migrations/  — SQL schema migrations
```

## Code Style

- Go: follow `gofmt`, no unnecessary comments
- TypeScript: no `any`, prefer explicit types
- Commit messages: imperative mood, e.g. `add dataset LRU eviction`

## Pull Request Guidelines

1. Open an issue first for significant changes
2. Keep PRs focused — one feature or fix per PR
3. Ensure `make lint` passes
4. Update docs if you change user-facing behavior
