---
id: upgrade
title: 版本升级
sidebar_position: 10
---

# 版本升级

## 控制平面

```bash
# 拉取新镜像或二进制文件
docker compose pull
docker compose up -d

# 或使用二进制文件
./muvee server  # 启动时自动应用新的数据库迁移
```

服务器启动时会自动应用数据库迁移。迁移是单向且幂等的（通过 `schema_migrations` 表追踪）。

## Agent 节点

Agent 是无状态的，可以随时重启。正在执行中的任务会在超时后由调度器重新入队。

```bash
# 重启 Agent 容器
docker restart muvee-agent
```

## 回滚

muvee 不支持自动数据库回滚。如需手动回滚，请恢复升级前备份的 PostgreSQL 数据。
