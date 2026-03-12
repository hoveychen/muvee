---
id: upgrade
title: Upgrading
sidebar_position: 10
---

# Upgrading

## Control Plane

```bash
# Pull new image or binary
docker compose pull
docker compose up -d

# Or with binary
./muvee server  # auto-applies new migrations on startup
```

Database migrations are applied automatically when the server starts. They are forward-only and idempotent (tracked in `schema_migrations` table).

## Agents

Agents are stateless and can be restarted at any time. In-flight tasks will be re-queued by the scheduler after a timeout.

```bash
# Restart agent container
docker restart muvee-agent
```

## Rolling Back

muvee does not support automatic database rollbacks. For manual rollback, restore your PostgreSQL backup taken before the upgrade.
