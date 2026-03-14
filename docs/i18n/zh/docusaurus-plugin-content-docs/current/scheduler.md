---
id: scheduler
title: 调度器与亲和性
sidebar_position: 5
---

# 调度器与亲和性

当触发部署时，muvee 需要选择在哪个部署节点上运行容器。调度器使用亲和性评分系统，综合考量数据集缓存命中率、可用存储空间和节点负载。

## 评分公式

```
score(node) =
  + cached_dataset_count × 10        W1：奖励缓存命中
  - missing_data_bytes   × 0.000001  W2：惩罚大量 rsync 同步
  + free_storage_bytes   × 0.0000001 W3：优先存储空间充裕的节点
```

得分最高的节点胜出。`readwrite` 类型的数据集不参与评分（它们使用直接 NFS 挂载，不影响本地存储）。

## LRU 驱逐

若得分最高的节点没有足够的空闲空间来存放缺失的 `dependency` 数据集，则触发 LRU 驱逐：

1. 按 `last_used_at ASC` 查询 `node_datasets`
2. 从最旧的数据集开始驱逐，直到 `free_bytes >= required_bytes`
3. 删除本地 `objects/{id}/v{ver}` 目录
4. 删除 `node_datasets` 记录

驱逐完成后，调度器继续为刚被驱逐的（以及新需要的）数据集执行 rsync 同步。

## 数据集 `last_used_at` 追踪

每当容器成功启动后，部署 Agent 都会调用控制平面接口，更新其所使用的所有 `dependency` 数据集的 `node_datasets.last_used_at` 时间戳。这确保了频繁使用的数据集能够持续保留在缓存中。
