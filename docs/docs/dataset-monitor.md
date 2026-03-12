---
id: dataset-monitor
title: Dataset Monitor
sidebar_position: 6
---

# Dataset Monitor

The Dataset Monitor tracks changes to NFS-backed datasets with file-level granularity — similar to `git log --follow` for data files.

## How It Works

```
Every 5 minutes (configurable):
    For each Dataset:
        1. Walk NFS path → build FileTree{path → {size, mtime, sha256}}
        2. Compare with previous snapshot
        3. Record added/modified/deleted events
        4. Bump dataset.version if changes found
        5. Write new snapshot record
```

## Checksum Strategy

To avoid SHA256-hashing every large file on every scan, the monitor uses a two-phase approach:

1. **Fast check** — compare `mtime + size`. If unchanged, skip.
2. **Slow verify** — if `mtime` changed, compute SHA256 to confirm actual content change.

## Manual Trigger

```bash
curl -X POST https://www.example.com/api/datasets/{id}/scan \
  -H "Authorization: Bearer $TOKEN"
```

## History Retention

File history records accumulate over time. To prevent unbounded growth, configure `MAX_HISTORY_DAYS` (default: no limit). Pruning runs automatically when new scans are written.

## UI: Single-File Timeline

The Datasets page provides a split-pane view:

- **Left**: file tree of the current snapshot
- **Right**: per-file change timeline showing `added` / `modified` / `deleted` events with checksum diffs and sizes

This gives a `git log`-style audit trail for any file in any dataset.
