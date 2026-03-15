/**
 * Vite 开发环境 Mock 插件
 *
 * 用法：VITE_MOCK=true npm run dev:mock
 * 所有 /api/* 和 /auth/* 请求由本插件拦截，无需启动后端服务。
 * 数据存储在内存中，重启 Vite 后重置为初始 seed 数据。
 */

import type { Plugin } from 'vite'
import type { IncomingMessage, ServerResponse } from 'http'

// ---------- 工具函数 ----------

function readBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  return new Promise((resolve) => {
    const chunks: Buffer[] = []
    req.on('data', (chunk: Buffer) => chunks.push(chunk))
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString()
      try {
        resolve(raw ? JSON.parse(raw) : {})
      } catch {
        resolve({})
      }
    })
    req.on('error', () => resolve({}))
  })
}

function send(res: ServerResponse, data: unknown, status = 200) {
  res.setHeader('Content-Type', 'application/json')
  res.statusCode = status
  res.end(JSON.stringify(data))
}

function sendError(res: ServerResponse, message: string, status = 400) {
  send(res, { error: message }, status)
}

function makeId() {
  return Math.random().toString(36).slice(2, 10)
}

function isoNow() {
  return new Date().toISOString()
}

// ---------- 简易路由器 ----------

type RouteParams = Record<string, string>
type RouteContext = {
  params: RouteParams
  body: Record<string, unknown>
  searchParams: URLSearchParams
}
type RouteHandler = (ctx: RouteContext) => unknown | Promise<unknown>

interface Route {
  method: string
  pattern: RegExp
  keys: string[]
  handler: RouteHandler
}

function defineRoute(method: string, path: string, handler: RouteHandler): Route {
  const keys: string[] = []
  const regexStr = path.replace(/:([^/]+)/g, (_: string, key: string) => {
    keys.push(key)
    return '([^/]+)'
  })
  return {
    method: method.toUpperCase(),
    pattern: new RegExp(`^${regexStr}$`),
    keys,
    handler,
  }
}

// ---------- Seed 数据（内存状态） ----------

const SEED_TIME = '2026-01-01T00:00:00Z'
const SEED_TIME2 = '2026-02-15T08:30:00Z'

interface MockUser {
  id: string
  email: string
  name: string
  avatar_url: string
  role: 'admin' | 'member'
  created_at: string
}

interface MockProject {
  id: string
  name: string
  git_url: string
  git_branch: string
  domain_prefix: string
  dockerfile_path: string
  owner_id: string
  auth_required: boolean
  auth_allowed_domains: string
  created_at: string
  updated_at: string
}

interface MockDataset {
  id: string
  name: string
  nfs_path: string
  size_bytes: number
  checksum: string
  version: number
  owner_id: string
  created_at: string
  updated_at: string
}

interface MockDeployment {
  id: string
  project_id: string
  image_tag: string
  commit_sha: string
  status: 'pending' | 'building' | 'deploying' | 'running' | 'failed' | 'stopped'
  node_id: string | null
  logs: string
  created_at: string
  updated_at: string
}

interface MockNode {
  id: string
  hostname: string
  role: 'builder' | 'deploy'
  max_storage_bytes: number
  used_storage_bytes: number
  last_seen_at: string
  created_at: string
}

interface MockDatasetSnapshot {
  id: string
  dataset_id: string
  scanned_at: string
  total_files: number
  total_size_bytes: number
  version: number
}

interface MockApiToken {
  id: string
  name: string
  last_used_at: string | null
  created_at: string
}

interface MockSecret {
  id: string
  name: string
  type: 'password' | 'ssh_key'
  created_at: string
  updated_at: string
}

interface MockProjectDataset {
  project_id: string
  dataset_id: string
  mount_mode: 'dependency' | 'readwrite'
}

interface MockProjectSecretBinding {
  secret_id: string
  secret_name: string
  secret_type: 'password' | 'ssh_key'
  env_var_name: string
  use_for_git: boolean
  git_username: string
}

interface MockFileHistory {
  id: string
  dataset_id: string
  file_path: string
  event_type: 'added' | 'modified' | 'deleted'
  old_size: number
  new_size: number
  old_checksum: string
  new_checksum: string
  snapshot_id: string
  occurred_at: string
}

interface MockContainerMetric {
  deployment_id: string
  collected_at: number   // epoch seconds
  cpu_percent: number
  mem_usage_bytes: number
  mem_limit_bytes: number
  net_rx_bytes: number
  net_tx_bytes: number
  block_read_bytes: number
  block_write_bytes: number
}

function buildInitialState() {
  const meUser: MockUser = {
    id: 'user-001',
    email: 'admin@example.com',
    name: 'Admin User',
    avatar_url: 'https://api.dicebear.com/7.x/avataaars/svg?seed=admin',
    role: 'admin',
    created_at: SEED_TIME,
  }

  const users: MockUser[] = [
    meUser,
    {
      id: 'user-002',
      email: 'alice@example.com',
      name: 'Alice Chen',
      avatar_url: 'https://api.dicebear.com/7.x/avataaars/svg?seed=alice',
      role: 'member',
      created_at: SEED_TIME2,
    },
  ]

  const projects: MockProject[] = [
    {
      id: 'proj-001',
      name: 'ml-training-service',
      git_url: 'https://github.com/example/ml-training-service.git',
      git_branch: 'main',
      domain_prefix: 'ml-train',
      dockerfile_path: 'Dockerfile',
      owner_id: 'user-001',
      auth_required: true,
      auth_allowed_domains: 'example.com',
      created_at: SEED_TIME,
      updated_at: SEED_TIME2,
    },
    {
      id: 'proj-002',
      name: 'data-pipeline-api',
      git_url: 'https://github.com/example/data-pipeline.git',
      git_branch: 'develop',
      domain_prefix: 'pipeline',
      dockerfile_path: 'docker/Dockerfile',
      owner_id: 'user-001',
      auth_required: false,
      auth_allowed_domains: '',
      created_at: SEED_TIME2,
      updated_at: SEED_TIME2,
    },
    {
      id: 'proj-003',
      name: 'inference-server',
      git_url: 'https://github.com/example/inference.git',
      git_branch: 'main',
      domain_prefix: 'infer',
      dockerfile_path: 'Dockerfile',
      owner_id: 'user-002',
      auth_required: true,
      auth_allowed_domains: 'example.com,corp.example.com',
      created_at: SEED_TIME2,
      updated_at: SEED_TIME2,
    },
  ]

  const datasets: MockDataset[] = [
    {
      id: 'ds-001',
      name: 'imagenet-subset',
      nfs_path: '/mnt/nfs/datasets/imagenet-subset',
      size_bytes: 52_428_800_000,
      checksum: 'sha256:abc123def456',
      version: 3,
      owner_id: 'user-001',
      created_at: SEED_TIME,
      updated_at: SEED_TIME2,
    },
    {
      id: 'ds-002',
      name: 'validation-set-v2',
      nfs_path: '/mnt/nfs/datasets/validation-v2',
      size_bytes: 1_073_741_824,
      checksum: 'sha256:789xyz000abc',
      version: 1,
      owner_id: 'user-001',
      created_at: SEED_TIME2,
      updated_at: SEED_TIME2,
    },
  ]

  const nodes: MockNode[] = [
    {
      id: 'node-001',
      hostname: 'builder-01.internal',
      role: 'builder',
      max_storage_bytes: 500_000_000_000,
      used_storage_bytes: 123_000_000_000,
      last_seen_at: new Date(Date.now() - 15_000).toISOString(),
      created_at: SEED_TIME,
    },
    {
      id: 'node-002',
      hostname: 'deploy-01.internal',
      role: 'deploy',
      max_storage_bytes: 200_000_000_000,
      used_storage_bytes: 45_000_000_000,
      last_seen_at: new Date(Date.now() - 8_000).toISOString(),
      created_at: SEED_TIME,
    },
  ]

  const tokens: MockApiToken[] = [
    {
      id: 'tok-001',
      name: 'CI/CD Pipeline',
      last_used_at: SEED_TIME2,
      created_at: SEED_TIME,
    },
    {
      id: 'tok-002',
      name: 'Local Dev',
      last_used_at: null,
      created_at: SEED_TIME2,
    },
  ]

  const secrets: MockSecret[] = [
    {
      id: 'sec-001',
      name: 'github-pat',
      type: 'password',
      created_at: SEED_TIME,
      updated_at: SEED_TIME,
    },
    {
      id: 'sec-002',
      name: 'deploy-ssh-key',
      type: 'ssh_key',
      created_at: SEED_TIME2,
      updated_at: SEED_TIME2,
    },
  ]

  const projectDatasets: MockProjectDataset[] = [
    { project_id: 'proj-001', dataset_id: 'ds-001', mount_mode: 'dependency' },
    { project_id: 'proj-001', dataset_id: 'ds-002', mount_mode: 'dependency' },
  ]

  const projectSecrets: MockProjectSecretBinding[] = [
    {
      secret_id: 'sec-001',
      secret_name: 'github-pat',
      secret_type: 'password',
      env_var_name: 'GITHUB_TOKEN',
      use_for_git: true,
      git_username: 'x-access-token',
    },
  ]

  const deployments: MockDeployment[] = [
    {
      id: 'dep-001',
      project_id: 'proj-001',
      image_tag: 'ml-train:sha-a1b2c3d',
      commit_sha: 'a1b2c3d4e5f6',
      status: 'running',
      node_id: 'node-002',
      logs: '[build] Step 1/8: FROM python:3.11-slim\n[build] Step 2/8: WORKDIR /app\n[build] Successfully built image\n[deploy] Container started on node deploy-01',
      created_at: SEED_TIME2,
      updated_at: SEED_TIME2,
    },
    {
      id: 'dep-002',
      project_id: 'proj-001',
      image_tag: 'ml-train:sha-0f9e8d7',
      commit_sha: '0f9e8d7c6b5a',
      status: 'stopped',
      node_id: 'node-002',
      logs: '[build] Successfully built image\n[deploy] Container stopped',
      created_at: SEED_TIME,
      updated_at: SEED_TIME,
    },
  ]

  const snapshots: MockDatasetSnapshot[] = [
    {
      id: 'snap-001',
      dataset_id: 'ds-001',
      scanned_at: SEED_TIME2,
      total_files: 128_450,
      total_size_bytes: 52_428_800_000,
      version: 3,
    },
    {
      id: 'snap-002',
      dataset_id: 'ds-001',
      scanned_at: SEED_TIME,
      total_files: 120_000,
      total_size_bytes: 48_000_000_000,
      version: 2,
    },
  ]

  const fileHistory: MockFileHistory[] = [
    {
      id: 'fh-001',
      dataset_id: 'ds-001',
      file_path: 'train/class_001/img_00001.jpg',
      event_type: 'added',
      old_size: 0,
      new_size: 102_400,
      old_checksum: '',
      new_checksum: 'sha256:aaa111',
      snapshot_id: 'snap-001',
      occurred_at: SEED_TIME2,
    },
    {
      id: 'fh-002',
      dataset_id: 'ds-001',
      file_path: 'train/class_002/img_00099.jpg',
      event_type: 'modified',
      old_size: 98_304,
      new_size: 102_400,
      old_checksum: 'sha256:bbb222',
      new_checksum: 'sha256:ccc333',
      snapshot_id: 'snap-001',
      occurred_at: SEED_TIME2,
    },
    {
      id: 'fh-003',
      dataset_id: 'ds-001',
      file_path: 'val/class_001/img_old.jpg',
      event_type: 'deleted',
      old_size: 65_536,
      new_size: 0,
      old_checksum: 'sha256:ddd444',
      new_checksum: '',
      snapshot_id: 'snap-001',
      occurred_at: SEED_TIME2,
    },
  ]

  // 为 dep-001 (proj-001 的运行中 deployment) 生成最近 60 条监控采样（每 30 秒一条）
  const containerMetrics: MockContainerMetric[] = (() => {
    const MEM_LIMIT = 4 * 1024 * 1024 * 1024  // 4 GiB
    const now = Math.floor(Date.now() / 1000)
    const samples: MockContainerMetric[] = []
    let netRx = 180 * 1024 * 1024     // 起始网络接收累计 180 MB
    let netTx = 42 * 1024 * 1024      // 起始网络发送累计 42 MB
    let blkRead = 2.4 * 1024 * 1024 * 1024  // 起始磁盘读取累计 2.4 GB
    let blkWrite = 600 * 1024 * 1024  // 起始磁盘写入累计 600 MB

    for (let i = 59; i >= 0; i--) {
      // CPU：ML 训练负载，波动在 35%~92% 之间，带周期性脉冲
      const cpuBase = 60 + 25 * Math.sin(i * 0.18) + 8 * Math.sin(i * 0.7)
      const cpu = Math.max(5, Math.min(99, cpuBase + (Math.random() - 0.5) * 10))

      // 内存：稳定在 2.8~3.4 GiB，缓慢增长
      const memUsage = Math.floor((2.8 + 0.006 * (59 - i) + (Math.random() - 0.5) * 0.12) * 1024 * 1024 * 1024)

      // 网络：累计递增，ML 服务接收多、发送少
      netRx += Math.floor((80 + Math.random() * 40) * 1024)   // 每样本 +80~120 KB
      netTx += Math.floor((8 + Math.random() * 6) * 1024)     // 每样本 +8~14 KB

      // 磁盘：读取量大（加载 checkpoint）、写入少（保存结果）
      blkRead += Math.floor((600 + Math.random() * 300) * 1024)  // 每样本 +600~900 KB
      blkWrite += Math.floor((20 + Math.random() * 30) * 1024)   // 每样本 +20~50 KB

      samples.push({
        deployment_id: 'dep-001',
        collected_at: now - i * 30,
        cpu_percent: parseFloat(cpu.toFixed(2)),
        mem_usage_bytes: memUsage,
        mem_limit_bytes: MEM_LIMIT,
        net_rx_bytes: Math.floor(netRx),
        net_tx_bytes: Math.floor(netTx),
        block_read_bytes: Math.floor(blkRead),
        block_write_bytes: Math.floor(blkWrite),
      })
    }
    // 返回最新在前（与真实 API 一致）
    return samples.reverse()
  })()

  return {
    me: meUser,
    users,
    projects,
    datasets,
    nodes,
    tokens,
    secrets,
    projectDatasets,
    projectSecrets,
    deployments,
    snapshots,
    fileHistory,
    containerMetrics,
  }
}

// ---------- 路由定义 ----------

function buildRoutes(state: ReturnType<typeof buildInitialState>): Route[] {
  return [
    // ---------- auth ----------
    defineRoute('POST', '/auth/logout', () => {
      return { ok: true }
    }),

    // ---------- /api/me ----------
    defineRoute('GET', '/api/me', () => state.me),

    // ---------- /api/users ----------
    defineRoute('GET', '/api/users', () => state.users),
    defineRoute('PUT', '/api/users/:id/role', ({ params, body }) => {
      const user = state.users.find((u) => u.id === params.id)
      if (!user) return null
      user.role = (body.role as 'admin' | 'member') ?? user.role
      return user
    }),

    // ---------- /api/tokens ----------
    defineRoute('GET', '/api/tokens', () => state.tokens),
    defineRoute('POST', '/api/tokens', ({ body }) => {
      const token: MockApiToken = {
        id: makeId(),
        name: (body.name as string) || 'New Token',
        last_used_at: null,
        created_at: isoNow(),
      }
      state.tokens.push(token)
      return { ...token, token: `mock-token-${makeId()}` }
    }),
    defineRoute('DELETE', '/api/tokens/:id', ({ params }) => {
      const idx = state.tokens.findIndex((t) => t.id === params.id)
      if (idx !== -1) state.tokens.splice(idx, 1)
      return {}
    }),

    // ---------- /api/secrets ----------
    defineRoute('GET', '/api/secrets', () => state.secrets),
    defineRoute('POST', '/api/secrets', ({ body }) => {
      const secret: MockSecret = {
        id: makeId(),
        name: (body.name as string) || 'new-secret',
        type: (body.type as 'password' | 'ssh_key') || 'password',
        created_at: isoNow(),
        updated_at: isoNow(),
      }
      state.secrets.push(secret)
      return secret
    }),
    defineRoute('DELETE', '/api/secrets/:id', ({ params }) => {
      const idx = state.secrets.findIndex((s) => s.id === params.id)
      if (idx !== -1) state.secrets.splice(idx, 1)
      return {}
    }),

    // ---------- /api/projects ----------
    defineRoute('GET', '/api/projects', () => state.projects),
    defineRoute('POST', '/api/projects', ({ body }) => {
      const proj: MockProject = {
        id: makeId(),
        name: (body.name as string) || 'new-project',
        git_url: (body.git_url as string) || '',
        git_branch: (body.git_branch as string) || 'main',
        domain_prefix: (body.domain_prefix as string) || makeId(),
        dockerfile_path: (body.dockerfile_path as string) || 'Dockerfile',
        owner_id: state.me.id,
        auth_required: (body.auth_required as boolean) ?? false,
        auth_allowed_domains: (body.auth_allowed_domains as string) || '',
        created_at: isoNow(),
        updated_at: isoNow(),
      }
      state.projects.push(proj)
      return proj
    }),
    defineRoute('GET', '/api/projects/:id', ({ params }) => {
      return state.projects.find((p) => p.id === params.id) ?? null
    }),
    defineRoute('PUT', '/api/projects/:id', ({ params, body }) => {
      const proj = state.projects.find((p) => p.id === params.id)
      if (!proj) return null
      Object.assign(proj, body, { updated_at: isoNow() })
      return proj
    }),
    defineRoute('DELETE', '/api/projects/:id', ({ params }) => {
      const idx = state.projects.findIndex((p) => p.id === params.id)
      if (idx !== -1) state.projects.splice(idx, 1)
      return {}
    }),

    // ---------- /api/projects/:id/datasets ----------
    defineRoute('GET', '/api/projects/:id/datasets', ({ params }) => {
      return state.projectDatasets.filter((pd) => pd.project_id === params.id)
    }),
    defineRoute('PUT', '/api/projects/:id/datasets', ({ params, body }) => {
      const incoming = body as unknown as MockProjectDataset[]
      const others = state.projectDatasets.filter((pd) => pd.project_id !== params.id)
      const updated = (Array.isArray(incoming) ? incoming : []).map((item) => ({
        ...item,
        project_id: params.id,
      }))
      state.projectDatasets.length = 0
      state.projectDatasets.push(...others, ...updated)
      return updated
    }),

    // ---------- /api/projects/:id/secrets ----------
    defineRoute('GET', '/api/projects/:id/secrets', ({ params }) => {
      return state.projectSecrets
        .filter((ps) => {
          const proj = state.projects.find((p) => p.id === params.id)
          return proj && ps.secret_id
        })
        .filter((_, i) => i < 10)
    }),
    defineRoute('PUT', '/api/projects/:id/secrets', ({ params, body }) => {
      type PartialBinding = Omit<MockProjectSecretBinding, 'secret_name' | 'secret_type'>
      const incoming = body as unknown as PartialBinding[]
      const enriched: MockProjectSecretBinding[] = (Array.isArray(incoming) ? incoming : []).map(
        (item) => {
          const sec = state.secrets.find((s) => s.id === item.secret_id)
          return {
            ...item,
            secret_name: sec?.name ?? item.secret_id,
            secret_type: sec?.type ?? 'password',
          }
        },
      )
      const others = state.projectSecrets.filter((ps) => {
        const belongs = enriched.some((e) => e.secret_id === ps.secret_id)
        return !belongs
      })
      state.projectSecrets.length = 0
      state.projectSecrets.push(...others, ...enriched)
      return enriched
    }),

    // ---------- /api/projects/:id/deploy ----------
    defineRoute('POST', '/api/projects/:id/deploy', ({ params }) => {
      const dep: MockDeployment = {
        id: makeId(),
        project_id: params.id,
        image_tag: `mock-image:sha-${makeId()}`,
        commit_sha: makeId() + makeId(),
        status: 'pending',
        node_id: null,
        logs: '[mock] Deployment triggered\n[build] Queued...',
        created_at: isoNow(),
        updated_at: isoNow(),
      }
      state.deployments.unshift(dep)
      // 模拟状态流转
      setTimeout(() => {
        dep.status = 'building'
        dep.logs += '\n[build] Building Docker image...'
        dep.updated_at = isoNow()
      }, 1500)
      setTimeout(() => {
        dep.status = 'deploying'
        dep.logs += '\n[build] Image built successfully\n[deploy] Deploying to node...'
        dep.node_id = 'node-002'
        dep.updated_at = isoNow()
      }, 3000)
      setTimeout(() => {
        dep.status = 'running'
        dep.logs += '\n[deploy] Container is running'
        dep.updated_at = isoNow()
        // 生成初始监控采样，后续每 30 秒追加一条
        const MEM_LIMIT = 4 * 1024 * 1024 * 1024
        const addSample = () => {
          const isRunning = state.deployments.find((d) => d.id === dep.id)?.status === 'running'
          if (!isRunning) return
          const prev = state.containerMetrics.find((m) => m.deployment_id === dep.id)
          const cpu = parseFloat((Math.random() * 60 + 20).toFixed(2))
          const memUsage = Math.floor((2.5 + Math.random() * 0.8) * 1024 * 1024 * 1024)
          const netRxDelta = Math.floor((60 + Math.random() * 60) * 1024)
          const netTxDelta = Math.floor((6 + Math.random() * 8) * 1024)
          const blkRDelta = Math.floor((400 + Math.random() * 400) * 1024)
          const blkWDelta = Math.floor((15 + Math.random() * 35) * 1024)
          state.containerMetrics.unshift({
            deployment_id: dep.id,
            collected_at: Math.floor(Date.now() / 1000),
            cpu_percent: cpu,
            mem_usage_bytes: memUsage,
            mem_limit_bytes: MEM_LIMIT,
            net_rx_bytes: (prev?.net_rx_bytes ?? 0) + netRxDelta,
            net_tx_bytes: (prev?.net_tx_bytes ?? 0) + netTxDelta,
            block_read_bytes: (prev?.block_read_bytes ?? 0) + blkRDelta,
            block_write_bytes: (prev?.block_write_bytes ?? 0) + blkWDelta,
          })
          setTimeout(addSample, 30_000)
        }
        addSample()
      }, 5000)
      return dep
    }),

    // ---------- /api/projects/:id/deployments ----------
    defineRoute('GET', '/api/projects/:id/deployments', ({ params }) => {
      return state.deployments.filter((d) => d.project_id === params.id)
    }),

    // ---------- /api/projects/:id/metrics ----------
    defineRoute('GET', '/api/projects/:id/metrics', ({ params, searchParams }) => {
      // 找到该 project 当前 running deployment 的 id
      const running = state.deployments.find(
        (d) => d.project_id === params.id && d.status === 'running',
      )
      if (!running) return []
      const limit = Math.min(1440, Math.max(1, parseInt(searchParams.get('limit') ?? '60', 10) || 60))
      return state.containerMetrics
        .filter((m) => m.deployment_id === running.id)
        .slice(0, limit)
    }),

    // ---------- /api/datasets ----------
    defineRoute('GET', '/api/datasets', () => state.datasets),
    defineRoute('POST', '/api/datasets', ({ body }) => {
      const ds: MockDataset = {
        id: makeId(),
        name: (body.name as string) || 'new-dataset',
        nfs_path: (body.nfs_path as string) || '/mnt/nfs/datasets/new',
        size_bytes: 0,
        checksum: '',
        version: 0,
        owner_id: state.me.id,
        created_at: isoNow(),
        updated_at: isoNow(),
      }
      state.datasets.push(ds)
      return ds
    }),
    defineRoute('GET', '/api/datasets/:id', ({ params }) => {
      return state.datasets.find((d) => d.id === params.id) ?? null
    }),
    defineRoute('PUT', '/api/datasets/:id', ({ params, body }) => {
      const ds = state.datasets.find((d) => d.id === params.id)
      if (!ds) return null
      Object.assign(ds, body, { updated_at: isoNow() })
      return ds
    }),
    defineRoute('DELETE', '/api/datasets/:id', ({ params }) => {
      const idx = state.datasets.findIndex((d) => d.id === params.id)
      if (idx !== -1) state.datasets.splice(idx, 1)
      return {}
    }),
    defineRoute('POST', '/api/datasets/:id/scan', ({ params }) => {
      const ds = state.datasets.find((d) => d.id === params.id)
      if (!ds) return null
      ds.version += 1
      ds.updated_at = isoNow()
      const snap: MockDatasetSnapshot = {
        id: makeId(),
        dataset_id: params.id,
        scanned_at: isoNow(),
        total_files: Math.floor(Math.random() * 200_000) + 1000,
        total_size_bytes: ds.size_bytes || Math.floor(Math.random() * 50_000_000_000),
        version: ds.version,
      }
      state.snapshots.push(snap)
      return snap
    }),
    defineRoute('GET', '/api/datasets/:id/snapshots', ({ params }) => {
      return state.snapshots
        .filter((s) => s.dataset_id === params.id)
        .sort((a, b) => b.version - a.version)
    }),
    defineRoute('GET', '/api/datasets/:id/history', ({ params, searchParams }) => {
      const file = searchParams.get('file')
      return state.fileHistory.filter(
        (h) => h.dataset_id === params.id && (!file || h.file_path === file),
      )
    }),

    // ---------- /api/nodes ----------
    defineRoute('GET', '/api/nodes', () => state.nodes),

    defineRoute('GET', '/api/nodes/:id/metrics', ({ params }) => {
      const node = state.nodes.find(n => n.id === params.id)
      if (!node) return null
      const now = Math.floor(Date.now() / 1000)
      return {
        node_id: node.id,
        collected_at: now - 15,
        cpu_percent: 30 + Math.random() * 40,
        mem_total_bytes: 32 * 1024 * 1024 * 1024,
        mem_used_bytes: Math.floor((8 + Math.random() * 16) * 1024 * 1024 * 1024),
        disk_total_bytes: node.max_storage_bytes,
        disk_used_bytes: node.used_storage_bytes,
        load1: 0.5 + Math.random() * 2,
        load5: 0.4 + Math.random() * 1.5,
        load15: 0.3 + Math.random() * 1,
      }
    }),
  ]
}

// ---------- Mock 身份切换器注入 HTML ----------

const SWITCHER_HTML = `
<style>
  #__mock-switcher {
    position: fixed;
    bottom: 16px;
    right: 16px;
    z-index: 99999;
    font-family: system-ui, sans-serif;
    font-size: 12px;
  }
  #__mock-switcher .badge {
    display: flex;
    align-items: center;
    gap: 6px;
    background: #1e293b;
    color: #f8fafc;
    border: 1px solid #334155;
    border-radius: 8px;
    padding: 6px 10px;
    cursor: pointer;
    user-select: none;
    box-shadow: 0 2px 8px rgba(0,0,0,0.4);
    transition: background 0.15s;
  }
  #__mock-switcher .badge:hover { background: #334155; }
  #__mock-switcher .avatar {
    width: 20px;
    height: 20px;
    border-radius: 50%;
  }
  #__mock-switcher .role-tag {
    font-size: 10px;
    padding: 1px 5px;
    border-radius: 4px;
    font-weight: 600;
    letter-spacing: 0.02em;
  }
  #__mock-switcher .role-tag.admin { background: #7c3aed; color: #ede9fe; }
  #__mock-switcher .role-tag.member { background: #0369a1; color: #e0f2fe; }
  #__mock-switcher .menu {
    position: absolute;
    bottom: calc(100% + 8px);
    right: 0;
    background: #1e293b;
    border: 1px solid #334155;
    border-radius: 8px;
    overflow: hidden;
    box-shadow: 0 4px 16px rgba(0,0,0,0.5);
    min-width: 200px;
    display: none;
  }
  #__mock-switcher .menu.open { display: block; }
  #__mock-switcher .menu-header {
    padding: 8px 12px;
    color: #94a3b8;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    border-bottom: 1px solid #334155;
  }
  #__mock-switcher .menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    cursor: pointer;
    color: #f8fafc;
    transition: background 0.1s;
  }
  #__mock-switcher .menu-item:hover { background: #334155; }
  #__mock-switcher .menu-item.active { background: #0f172a; }
  #__mock-switcher .menu-item img { width: 24px; height: 24px; border-radius: 50%; }
  #__mock-switcher .menu-item .info { flex: 1; }
  #__mock-switcher .menu-item .name { font-weight: 500; font-size: 12px; }
  #__mock-switcher .menu-item .email { color: #94a3b8; font-size: 10px; }
  #__mock-switcher .check { color: #4ade80; font-size: 14px; }
</style>
<div id="__mock-switcher">
  <div class="menu" id="__mock-menu">
    <div class="menu-header">切换 Mock 身份</div>
  </div>
  <div class="badge" id="__mock-badge" onclick="__mockToggleMenu()">
    <img class="avatar" id="__mock-avatar" src="" alt="">
    <span id="__mock-name">Loading...</span>
    <span class="role-tag" id="__mock-role-tag"></span>
    <span style="color:#94a3b8">▲</span>
  </div>
</div>
<script>
  var __mockCurrentId = null;

  async function __mockLoadState() {
    try {
      const [meRes, usersRes] = await Promise.all([
        fetch('/_mock/me'),
        fetch('/_mock/users'),
      ]);
      const me = await meRes.json();
      const users = await usersRes.json();
      __mockCurrentId = me.id;

      document.getElementById('__mock-avatar').src = me.avatar_url;
      document.getElementById('__mock-name').textContent = me.name;
      const roleTag = document.getElementById('__mock-role-tag');
      roleTag.textContent = me.role;
      roleTag.className = 'role-tag ' + me.role;

      const menu = document.getElementById('__mock-menu');
      const header = menu.querySelector('.menu-header');
      menu.innerHTML = '';
      menu.appendChild(header);
      users.forEach(function(u) {
        const item = document.createElement('div');
        item.className = 'menu-item' + (u.id === me.id ? ' active' : '');
        item.innerHTML =
          '<img src="' + u.avatar_url + '" alt="">' +
          '<div class="info"><div class="name">' + u.name + '</div><div class="email">' + u.email + '</div></div>' +
          '<span class="role-tag ' + u.role + '">' + u.role + '</span>' +
          (u.id === me.id ? '<span class="check">✓</span>' : '');
        item.onclick = function() { __mockSwitchUser(u.id); };
        menu.appendChild(item);
      });
    } catch(e) { console.warn('[mock]', e); }
  }

  async function __mockSwitchUser(userId) {
    if (userId === __mockCurrentId) { __mockToggleMenu(); return; }
    await fetch('/_mock/switch-user', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ userId: userId }),
    });
    window.location.reload();
  }

  function __mockToggleMenu() {
    const menu = document.getElementById('__mock-menu');
    menu.classList.toggle('open');
  }

  document.addEventListener('click', function(e) {
    const sw = document.getElementById('__mock-switcher');
    if (sw && !sw.contains(e.target)) {
      document.getElementById('__mock-menu').classList.remove('open');
    }
  });

  __mockLoadState();
</script>
`

// ---------- 插件导出 ----------

export function mockPlugin(): Plugin {
  const state = buildInitialState()
  const routes = buildRoutes(state)

  return {
    name: 'vite-mock-api',

    transformIndexHtml(html) {
      return html.replace('</body>', SWITCHER_HTML + '</body>')
    },

    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        const rawUrl = req.url ?? '/'
        const urlObj = new URL(rawUrl, 'http://localhost')
        const pathname = urlObj.pathname
        const method = (req.method ?? 'GET').toUpperCase()

        // ---- /_mock/* 身份切换专用端点 ----
        if (pathname === '/_mock/me' && method === 'GET') {
          return send(res, state.me)
        }
        if (pathname === '/_mock/users' && method === 'GET') {
          return send(res, state.users)
        }
        if (pathname === '/_mock/switch-user' && method === 'POST') {
          const body = await readBody(req)
          const target = state.users.find((u) => u.id === body.userId)
          if (!target) return sendError(res, 'user not found', 404)
          state.me = target
          return send(res, { ok: true, me: target })
        }

        // 只拦截 /api/* 和 /auth/* 路径
        if (!pathname.startsWith('/api/') && !pathname.startsWith('/auth/')) {
          return next()
        }

        for (const route of routes) {
          if (route.method !== method) continue
          const match = route.pattern.exec(pathname)
          if (!match) continue

          const params: RouteParams = {}
          route.keys.forEach((key, i) => {
            params[key] = decodeURIComponent(match[i + 1])
          })

          const body = ['POST', 'PUT', 'PATCH'].includes(method) ? await readBody(req) : {}
          const ctx: RouteContext = { params, body, searchParams: urlObj.searchParams }

          try {
            const result = await route.handler(ctx)
            if (result === null) {
              sendError(res, 'not found', 404)
            } else {
              send(res, result)
            }
          } catch (err) {
            sendError(res, String(err), 500)
          }
          return
        }

        // 路径匹配但无对应路由时返回 404
        sendError(res, `mock: no handler for ${method} ${pathname}`, 404)
      })
    },
  }
}
