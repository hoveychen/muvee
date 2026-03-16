---
id: architecture
title: 架构设计
sidebar_position: 4
---

# 架构设计

## 总览

![muvee 系统架构总览](/img/arch-overview.png)

```
┌─────────────────────────────────────────────┐
│              控制平面                         │
│  ┌──────────┐  ┌──────────┐  ┌───────────┐  │
│  │API Server│  │Scheduler │  │  Monitor  │  │
│  │ (Go+Chi) │  │亲和调度  │  │NFS 轮询   │  │
│  └────┬─────┘  └────┬─────┘  └───────────┘  │
│       │             │                         │
│  ┌────▼─────────────▼──┐  ┌───────────────┐  │
│  │     PostgreSQL       │  │  镜像分发     │  │
│  │   （元数据库）        │  │   Registry    │  │
│  └─────────────────────┘  └───────────────┘  │
│  ┌──────────────────────────────────────────┐ │
│  │   Traefik v3  （反向代理 + HTTPS）        │ │
│  └──────────────────────────────────────────┘ │
└─────────────────────────────────────────────┘
          │ 长轮询任务              │ 推送镜像
          ▼                        ▼
┌──────────────────┐        ┌──────────────────┐
│   构建节点        │        │   部署节点        │
│  git clone        │        │  rsync NFS→本地  │
│  docker buildx    │        │  LRU 缓存         │
│  push image       │        │  docker run       │
└──────────────────┘        └──────────────────┘
                                       │
                              ┌────────▼────────┐
                              │  NFS 数据存储    │
                              │  /nfs/warehouse  │
                              └─────────────────┘
```

## 组件说明

### 控制平面（`cmd/server`）

- **API Server**（`internal/api`）— Chi 路由器、JWT 认证中间件、Projects / Datasets / Deployments / Nodes / Users 的 REST 处理器
- **Scheduler**（`internal/scheduler`）— 亲和性评分、LRU 驱逐触发、任务分发
- **Monitor**（`internal/monitor`）— 定期扫描 NFS 路径、校验和差分、文件历史记录
- **Auth**（`internal/auth`）— Google OIDC、JWT 签发、RBAC 中间件

### Agent（`cmd/agent`）

部署在工作节点上的单一二进制文件。角色通过 `NODE_ROLE` 环境变量配置：

- **builder**（构建节点）— 轮询构建任务，执行 `git clone` + `docker buildx build` + `docker push`
- **deploy**（部署节点）— 轮询部署任务，执行数据同步（rsync / NFS bind-mount）、`docker run -p 0:{port}`，并将 `host_port` 报告给控制平面

### ForwardAuth 服务（`cmd/authservice`）

独立的 HTTP 服务，Traefik 通过 ForwardAuth 中间件调用它来强制执行项目级 Google 身份验证。

## 数据流：部署过程

![muvee 部署流程](/img/deploy-flow.png)

```
用户点击"部署"
    │
    ▼
API 创建 Deployment 记录（status=pending）
    │
    ▼
Scheduler.DispatchBuild()
  → 选择一个活跃的构建节点
  → 创建构建 Task
    │
    ▼
构建 Agent 轮询并接取任务
  → git clone --depth=1
  → docker buildx build
  → docker push registry/{project}:{sha}
  → POST /api/agent/tasks/{id}/complete
    │
    ▼
服务端后台循环检测到构建完成
  → Scheduler.DispatchDeploy()
  → 按亲和性为部署节点打分
  → 创建部署 Task
    │
    ▼
部署 Agent 接取任务
  → 对每个 dependency 数据集：
      rsync NFS → /muvee/data/objects/{id}/v{ver}
      symlink → /muvee/data/mounts/{deployment_id}/{name}
  → 对每个 readwrite 数据集：
      直接 NFS bind-mount
  → docker run -p 0:{container_port} -v /muvee/data/mounts/...
  → docker port → 发现已分配的 host_port
  → POST /api/agent/tasks/{id}/complete { host_port }
    │
    ▼
控制平面将 host_ip + host_port 存入 deployments 表
    │
    ▼
Traefik HTTP provider 每 5 秒轮询 GET /api/traefik/config
  → {project}.domain.com → http://{node_ip}:{host_port}
```

## 亲和性评分与数据集挂载模式

![数据集智能调度与挂载模式](/img/dataset-scheduling.png)

在调度部署任务时，每个活跃的部署节点会获得一个评分：

```
score(node) =
  + cached_dataset_count × 10       # 奖励缓存命中，减少 rsync 开销
  - missing_bytes × 0.000001        # 惩罚需要大量同步的情况
  + free_storage_bytes × 0.0000001  # 优先选择存储空间更充裕的节点
```

若得分最高的节点剩余空间不足，则先触发 LRU 数据集缓存驱逐（按 `last_used_at` 从旧到新）。

## 数据集模式

| 模式 | 存储 | LRU | NFS 依赖 | 容器挂载 |
|---|---|---|---|---|
| `dependency` | 本地（rsync） | 是 | 仅读取路径 | 只读 bind-mount 本地副本（`:ro`） |
| `readwrite` | 无 | 否 | 运行时直接访问 | 读写 bind-mount NFS 路径（`:rw`） |
