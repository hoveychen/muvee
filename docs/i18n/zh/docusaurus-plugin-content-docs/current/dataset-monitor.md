---
id: dataset-monitor
title: 数据集监控
sidebar_position: 6
---

# 数据集监控

数据集监控以文件级粒度追踪 NFS 数据集的变更——类似于数据文件的 `git log --follow`。

## 工作原理

```
每 5 分钟（可配置）：
    对每个数据集：
        1. 遍历 NFS 路径 → 构建 FileTree{路径 → {大小, mtime, sha256}}
        2. 与上一次快照对比
        3. 记录新增/修改/删除事件
        4. 若发现变更则递增 dataset.version
        5. 写入新的快照记录
```

## 校验和策略

为避免每次扫描都对所有大文件计算 SHA256，监控器采用两阶段方案：

1. **快速检查** — 比较 `mtime + size`。若未变化，直接跳过。
2. **慢速验证** — 若 `mtime` 发生变化，则计算 SHA256 以确认内容是否实际变更。

## 手动触发扫描

```bash
curl -X POST https://www.example.com/api/datasets/{id}/scan \
  -H "Authorization: Bearer $TOKEN"
```

## 历史记录保留

文件历史记录会持续累积。为防止无限增长，可配置 `MAX_HISTORY_DAYS`（默认：无限制）。清理操作会在每次新扫描写入时自动执行。

## UI：单文件时间线

数据集页面提供分栏视图：

- **左侧**：当前快照的文件树
- **右侧**：逐文件变更时间线，展示 `added`（新增）/ `modified`（修改）/ `deleted`（删除）事件，附带校验和差异和文件大小

这为每个数据集中的任意文件提供了类似 `git log` 的审计追踪。
