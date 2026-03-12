---
id: scheduler
title: Scheduler & Affinity
sidebar_position: 5
---

# Scheduler & Affinity

When a deployment is triggered, muvee must choose which deploy node to run the container on. The scheduler uses an affinity scoring system that balances dataset cache hits, available storage, and node load.

## Scoring Formula

```
score(node) =
  + cached_dataset_count × 10        W1: reward cache hits
  - missing_data_bytes   × 0.000001  W2: penalize large rsyncs
  + free_storage_bytes   × 0.0000001 W3: prefer storage headroom
```

The node with the highest score wins. `readwrite` datasets are excluded from scoring (they use direct NFS mounts and do not affect local storage).

## LRU Eviction

If the winning node lacks enough free space to hold the missing `dependency` datasets, LRU eviction kicks in:

1. Query `node_datasets` ordered by `last_used_at ASC`
2. Evict oldest datasets until `free_bytes >= required_bytes`
3. Delete the local `objects/{id}/v{ver}` directories
4. Remove `node_datasets` records

After eviction, the scheduler proceeds with rsync for the now-evicted (and newly required) datasets.

## Dataset `last_used_at` Tracking

Every time a container is successfully started, the deploy agent calls the control plane to update `node_datasets.last_used_at` for all dependency datasets it used. This ensures frequently-used datasets stay cached.
