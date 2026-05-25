import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Rocket, Settings, Database, KeyRound, HardDrive, ChevronDown, ChevronUp, Trash2, ArrowLeft, Link2, Link2Off, ExternalLink, Download, FolderOpen, File, Activity, GitBranch, Copy, Check, Key, Plus, Eye, EyeOff, HelpCircle, Shield, Users, Palette } from 'lucide-react'
import { api } from '../lib/api'
import type { ApiToken, CreatedApiToken, ContainerMetric, Dataset, Deployment, InvitationLink, InvitationLinkUse, Node as DeployNode, Project, ProjectAccessRequest, ProjectAccessUser, ProjectAlias, ProjectDataset, ProjectSecretBinding, ProjectTraffic, ProjectVisit, Secret, User, WorkspaceEntry, RepoTreeEntry, RepoCommit, RepoBranch } from '../lib/types'
import { statusColor, timeAgo, formatBytes, isValidDomainPrefix, resolveDatasetPath } from '../lib/utils'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../lib/auth'

const MONO = 'var(--font-mono)'

type Tab = 'deploy' | 'config' | 'branding' | 'auth' | 'users' | 'datasets' | 'secrets' | 'tokens' | 'workspace' | 'repository' | 'traffic'

/* Map deployment status → badge CSS class */
const statusBadgeClass = (status: string) => {
  switch (status) {
    case 'running': return 'badge badge-success'
    case 'building': return 'badge badge-warning'
    case 'deploying': return 'badge badge-info'
    case 'failed': return 'badge badge-danger'
    case 'stopped':
    case 'pending':
    default: return 'badge badge-neutral'
  }
}

export default function ProjectDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const { user: me } = useAuth()
  const isAdmin = me?.role === 'admin'
  const [project, setProject] = useState<Project | null>(null)
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [datasets, setDatasets] = useState<Dataset[]>([])
  const [projectDatasets, setProjectDatasets] = useState<ProjectDataset[]>([])
  const [availableDatasets, setAvailableDatasets] = useState<Dataset[]>([])
  const [projectSecrets, setProjectSecrets] = useState<ProjectSecretBinding[]>([])
  const [allSecrets, setAllSecrets] = useState<Secret[]>([])
  const [datasetBasePath, setDatasetBasePath] = useState('')
  const [baseDomain, setBaseDomain] = useState('')
  const [tab, setTab] = useState<Tab>('deploy')
  // Switch to traffic tab when a tunnel project loads
  const [tabInitialized, setTabInitialized] = useState(false)
  const [deploying, setDeploying] = useState(false)
  const [editForm, setEditForm] = useState<Partial<Project>>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const pollRef = useRef<ReturnType<typeof setInterval>>(null)

  useEffect(() => {
    if (!id) return
    api.projects.get(id).then(p => {
      setProject(p); setEditForm(p)
      if (!tabInitialized && p.project_type === 'domain_only') { setTab('traffic'); setTabInitialized(true) }
    })
    api.projects.deployments(id).then(setDeployments)
    api.projects.datasets(id).then(setProjectDatasets)
    api.datasets.list().then(setAvailableDatasets)
    api.projects.secrets(id).then(setProjectSecrets)
    api.secrets.list().then(setAllSecrets)
    api.runtime.config()
      .then(cfg => {
        setDatasetBasePath(cfg.dataset_nfs_base_path || '')
        setBaseDomain(cfg.base_domain || '')
      })
      .catch(() => setDatasetBasePath(''))
    pollRef.current = setInterval(() => {
      api.projects.deployments(id).then(setDeployments)
    }, 5000)
    return () => { if (pollRef.current) clearInterval(pollRef.current) }
  }, [id])

  const handleDeploy = async () => {
    if (!id) return
    setDeploying(true)
    try {
      await api.projects.deploy(id)
      const ds = await api.projects.deployments(id)
      setDeployments(ds)
    } finally {
      setDeploying(false)
    }
  }

  const handleSave = async () => {
    if (!id) return
    setSaving(true)
    setSaveError(null)
    try {
      const updated = await api.projects.update(id, editForm)
      setProject(updated)
    } catch (err) {
      setSaveError((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async () => {
    if (!id || !confirm(t('projectDetail.config.deleteConfirm'))) return
    await api.projects.delete(id)
    navigate('/projects')
  }

  const handleChangeOwner = async (newOwnerId: string) => {
    if (!id) return
    const updated = await api.projects.changeOwner(id, newOwnerId)
    setProject(updated)
    setEditForm(updated)
  }

  const toggleDataset = async (dsId: string, mode: 'dependency' | 'readwrite') => {
    if (!id) return
    const exists = projectDatasets.find(pd => pd.dataset_id === dsId)
    let updated: ProjectDataset[]
    if (exists) {
      updated = projectDatasets.filter(pd => pd.dataset_id !== dsId)
    } else {
      updated = [...projectDatasets, { project_id: id, dataset_id: dsId, mount_mode: mode }]
    }
    const result = await api.projects.setDatasets(id, updated)
    setProjectDatasets(result)
  }

  const updateMountMode = async (dsId: string, mode: 'dependency' | 'readwrite') => {
    if (!id) return
    const updated = projectDatasets.map(pd =>
      pd.dataset_id === dsId ? { ...pd, mount_mode: mode } : pd
    )
    const result = await api.projects.setDatasets(id, updated)
    setProjectDatasets(result)
  }

  if (!project) return <div style={{ color: 'var(--fg-muted)', padding: '2rem' }}>{t('projects.loading')}</div>

  const isTunnel = project.project_type === 'domain_only'
  const isCompose = project.project_type === 'compose'
  const isImage = project.project_type === 'image'
  const latestDeploy = deployments[0]
  const color = isTunnel ? 'var(--accent)' : statusColor(latestDeploy?.status ?? 'pending')

  return (
    <div className="page-enter">
      {/* Header */}
      <div className="page-header">
        <button
          onClick={() => navigate('/projects')}
          className="flex items-center gap-1 mb-4 text-sm transition-colors"
          style={{ color: 'var(--fg-muted)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
        >
          <ArrowLeft size={14} /> {t('projectDetail.backToProjects')}
        </button>
        <div className="flex items-center justify-between" style={{ flexWrap: 'wrap', gap: '8px' }}>
          <div className="flex items-center gap-3" style={{ minWidth: 0, flex: '1 1 200px' }}>
            <div
              className="w-2.5 h-2.5 rounded-full"
              style={{ background: color, flexShrink: 0 }}
            />
            <h1 className="page-title" style={{ fontSize: '1.5rem', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
              {project.name}
            </h1>
          </div>
          <div className="flex items-center gap-2" style={{ flexWrap: 'wrap' }}>
            <a
              href={`https://${project.domain_prefix}.${baseDomain}`}
              target="_blank"
              rel="noopener noreferrer"
              className="btn-secondary flex items-center gap-2"
              style={{ textDecoration: 'none' }}
            >
              <ExternalLink size={14} />
              {t('projectDetail.visit')}
            </a>
            {!isTunnel && (
            <button
              onClick={handleDeploy}
              disabled={deploying}
              className={deploying ? 'btn-secondary flex items-center gap-2' : 'btn-primary flex items-center gap-2'}
              style={deploying ? { cursor: 'not-allowed' } : {}}
            >
              <Rocket size={14} />
              {deploying ? t('projectDetail.triggering') : t('projectDetail.deploy')}
            </button>
            )}
          </div>
        </div>
        <div className="page-subtitle" style={{ marginTop: '0.4rem', marginLeft: '1.75rem' }}>
          <span style={{ fontFamily: MONO }}>{project.domain_prefix}.{baseDomain}</span>
        </div>
        {!isTunnel && project.git_source === 'hosted' && project.git_push_url && (
          <PushUrlBadge url={project.git_push_url} projectId={project.id} />
        )}
        {isTunnel && (
          <div style={{ marginTop: '0.5rem', marginLeft: '1.75rem', fontSize: '0.8125rem', color: 'var(--fg-muted)', fontFamily: MONO, background: 'var(--bg-hover)', padding: '0.5rem 0.75rem', borderRadius: '6px', display: 'inline-block' }}>
            {t('projectDetail.tunnelHint', { name: project.name })}
          </div>
        )}
        {isCompose && (
          <div style={{ marginTop: '0.5rem', marginLeft: '1.75rem', fontSize: '0.8125rem', color: 'var(--fg-muted)', fontFamily: MONO, background: 'var(--bg-hover)', padding: '0.5rem 0.75rem', borderRadius: '6px', display: 'inline-block' }}>
            {t('projectDetail.composeHint', {
              service: project.expose_service ?? '',
              port: project.expose_port ?? 0,
              pinned: project.pinned_node_id ? t('projectDetail.composePinned') : t('projectDetail.composeUnpinned'),
            })}
          </div>
        )}
        {isImage && (
          <div style={{ marginTop: '0.5rem', marginLeft: '1.75rem', fontSize: '0.8125rem', color: 'var(--fg-muted)', fontFamily: MONO, background: 'var(--bg-hover)', padding: '0.5rem 0.75rem', borderRadius: '6px', display: 'inline-block' }}>
            {t('projectDetail.imageHint', {
              ref: project.image_ref ?? '',
              port: project.container_port ?? 0,
            })}
          </div>
        )}
      </div>

      {/* Tabs */}
      <div className="flex gap-0 mb-0 project-tabs" style={{ borderBottom: '1px solid var(--border)', overflowX: 'auto', WebkitOverflowScrolling: 'touch' }}>
        {(isTunnel ? [
          ['traffic', Activity, t('projectDetail.tabs.traffic')],
          ['config', Settings, t('projectDetail.tabs.config')],
          ['branding', Palette, t('projectDetail.tabs.branding')],
          ['auth', Shield, t('projectDetail.tabs.auth')],
          ['users', Users, 'Users'],
          ['tokens', Key, t('projectDetail.tabs.tokens')],
        ] as const : [
          ['deploy', Rocket, t('projectDetail.tabs.deployments')],
          ['config', Settings, t('projectDetail.tabs.config')],
          ['branding', Palette, t('projectDetail.tabs.branding')],
          ['auth', Shield, t('projectDetail.tabs.auth')],
          ['users', Users, 'Users'] as const,
          ...(project.git_source === 'hosted' ? [['repository', GitBranch, t('projectDetail.tabs.repository')] as const] : []),
          // Datasets and workspace are not wired up for compose projects: the
          // agent runs `docker compose up` directly without NFS mounts or a
          // singleton container the user can browse into.
          ...(isCompose ? [] : [
            ['datasets', Database, t('projectDetail.tabs.datasets')] as const,
          ]),
          ['secrets', KeyRound, t('projectDetail.tabs.secrets')],
          ['tokens', Key, t('projectDetail.tabs.tokens')],
          ...(isCompose ? [] : [
            ['workspace', HardDrive, t('projectDetail.tabs.workspace')] as const,
          ]),
        ] as const).map(([key, Icon, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            data-active={tab === key}
            className="flex items-center gap-2 px-4 py-3 text-sm transition-all duration-150"
            style={{
              color: tab === key ? 'var(--fg-primary)' : 'var(--fg-muted)',
              background: 'none',
              border: 'none',
              borderBottom: tab === key ? '2px solid var(--accent)' : '2px solid transparent',
              cursor: 'pointer',
              marginBottom: '-1px',
              fontWeight: tab === key ? 600 : 400,
              fontSize: '0.875rem',
              flexShrink: 0,
              whiteSpace: 'nowrap',
            }}
          >
            <Icon size={15} />
            {label}
          </button>
        ))}
      </div>

      <div className="mt-6">
        {tab === 'deploy' && id && (
          <DeployTab deployments={deployments} projectId={id} />
        )}
        {tab === 'config' && (
          <ConfigTab
            form={editForm}
            onChange={setEditForm}
            onSave={handleSave}
            onDelete={handleDelete}
            saving={saving}
            saveError={saveError}
            isAdmin={isAdmin}
            onChangeOwner={handleChangeOwner}
          />
        )}
        {tab === 'branding' && (
          <BrandingTab
            form={editForm}
            onChange={setEditForm}
            onSave={handleSave}
            saving={saving}
            saveError={saveError}
          />
        )}
        {tab === 'auth' && (
          <AuthTab
            form={editForm}
            onChange={setEditForm}
            onSave={handleSave}
            saving={saving}
            saveError={saveError}
          />
        )}
        {tab === 'users' && id && (
          <UsersTab
            projectId={id}
            form={editForm}
            onChange={setEditForm}
            onSave={handleSave}
            saving={saving}
            saveError={saveError}
            ownerName={project.owner_name || project.owner_email || ''}
            ownerEmail={project.owner_email || ''}
            ownerAvatarURL={project.owner_avatar_url || ''}
            domainPrefix={project.domain_prefix || ''}
            baseDomain={baseDomain}
          />
        )}
        {tab === 'datasets' && (
          <DatasetsTab
            available={availableDatasets}
            selected={projectDatasets}
            datasetBasePath={datasetBasePath}
            onToggle={toggleDataset}
            onUpdateMode={updateMountMode}
          />
        )}
        {tab === 'secrets' && id && (
          <SecretsTab
            projectId={id}
            allSecrets={allSecrets}
            bindings={projectSecrets}
            onBindingsChange={setProjectSecrets}
          />
        )}
        {tab === 'tokens' && id && (
          <TokensTab projectId={id} />
        )}
        {tab === 'workspace' && id && (
          <WorkspaceTab
            projectId={id}
            volumeMountPath={project.volume_mount_path}
          />
        )}
        {tab === 'repository' && id && project.git_source === 'hosted' && (
          <RepoTab projectId={id} />
        )}
        {tab === 'traffic' && id && (
          <TunnelTrafficTab projectId={id} />
        )}
      </div>
    </div>
  )
}

function DeployTab({ deployments, projectId }: { deployments: Deployment[]; projectId: string }) {
  const [expanded, setExpanded] = useState<string | null>(deployments[0]?.id ?? null)
  const [metrics, setMetrics] = useState<ContainerMetric[]>([])
  const [traffic, setTraffic] = useState<ProjectTraffic[]>([])
  const { t } = useTranslation()

  // Fetch metrics for the running deployment when the tab mounts or deployments change.
  useEffect(() => {
    const running = deployments.find(d => d.status === 'running')
    if (!running) { setMetrics([]); return }
    api.projects.metrics(projectId, 60).then(setMetrics).catch(() => setMetrics([]))
    const iv = setInterval(() => {
      api.projects.metrics(projectId, 60).then(setMetrics).catch(() => {})
    }, 30_000)
    return () => clearInterval(iv)
  }, [projectId, deployments])

  // Fetch traffic history (independent of running state — past requests are still interesting).
  useEffect(() => {
    api.projects.traffic(projectId, 100).then(setTraffic).catch(() => setTraffic([]))
    const iv = setInterval(() => {
      api.projects.traffic(projectId, 100).then(setTraffic).catch(() => {})
    }, 15_000)
    return () => clearInterval(iv)
  }, [projectId])

  if (deployments.length === 0) {
    return (
      <div className="py-12 text-center" style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
        {t('projectDetail.noDeployments')}
      </div>
    )
  }

  const runningDeploymentId = deployments.find(d => d.status === 'running')?.id

  return (
    <div>
      {/* Metrics panel — shown when there is a running deployment with data */}
      {runningDeploymentId && metrics.length > 0 && (
        <MetricsPanel metrics={metrics} />
      )}
      {/* Traffic panel — recent HTTP requests observed by Traefik */}
      <TrafficPanel traffic={traffic} />
      <div className="card" style={{ overflow: 'hidden', marginTop: '1.5rem' }}>
        {deployments.map((d, i) => {
          const isOpen = expanded === d.id
          return (
            <div key={d.id} style={{ background: 'var(--bg-card)', borderBottom: i < deployments.length - 1 ? '1px solid var(--border)' : 'none' }}>
              <button
                className="w-full flex items-center gap-4 px-5 py-3.5 text-left transition-colors"
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'inherit' }}
                onClick={() => setExpanded(isOpen ? null : d.id)}
                onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--bg-hover)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'transparent' }}
              >
                <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusColor(d.status), flexShrink: 0 }} className={d.status === 'running' || d.status === 'building' ? 'status-running' : ''} />
                <div className="flex-1 min-w-0">
                  <div style={{ fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)' }}>
                    {d.commit_sha ? d.commit_sha.slice(0, 12) : d.id.slice(0, 8)}
                    {d.image_tag && (
                      <span style={{ color: 'var(--fg-muted)', marginLeft: '1rem', fontFamily: MONO }}>
                        {d.image_tag.split(':').pop()}
                      </span>
                    )}
                  </div>
                </div>
                <span className={statusBadgeClass(d.status)}>
                  {d.status}
                </span>
                {d.oom_killed && (
                  <span className="badge badge-danger" title="Container was OOM killed">
                    OOM
                  </span>
                )}
                {d.restart_count > 0 && (
                  <span className="badge badge-warning" title={`Container restarted ${d.restart_count} times`}>
                    ↺{d.restart_count}
                  </span>
                )}
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
                  {timeAgo(d.created_at)}
                </div>
                {isOpen ? <ChevronUp size={14} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} /> : <ChevronDown size={14} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />}
              </button>
              {isOpen && d.logs && (
                <div
                  className="terminal-scroll"
                  style={{
                    background: 'var(--bg-base)',
                    borderTop: '1px solid var(--border)',
                    padding: '1rem 1.5rem',
                    maxHeight: '360px',
                    overflowY: 'auto',
                    fontFamily: MONO,
                    fontSize: '0.8125rem',
                    color: '#adbac7',
                    lineHeight: '1.6',
                  }}
                >
                  {d.logs.split('\n').map((line, i) => (
                    <div key={i} className="log-line" style={{ animationDelay: `${i * 8}ms` }}>
                      {line || ' '}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

// ─── Metrics Panel ────────────────────────────────────────────────────────────

function MetricsPanel({ metrics }: { metrics: ContainerMetric[] }) {
  const { t } = useTranslation()
  const latest = metrics[0]

  const cpuColor = latest.cpu_percent > 80 ? 'var(--danger)' : latest.cpu_percent > 50 ? 'var(--warning)' : 'var(--accent)'
  const memPct = latest.mem_limit_bytes > 0
    ? (latest.mem_usage_bytes / latest.mem_limit_bytes) * 100
    : 0
  const memColor = memPct > 85 ? 'var(--danger)' : memPct > 65 ? 'var(--warning)' : 'var(--accent)'

  // Derive per-sample deltas for net/block I/O (show rate: bytes since last sample).
  // metrics is sorted newest-first; so metrics[0] is latest, metrics[1] is previous.
  const prev = metrics[1]
  const elapsedSec = prev ? Math.max(1, latest.collected_at - prev.collected_at) : 30
  const netRxRate = prev ? Math.max(0, latest.net_rx_bytes - prev.net_rx_bytes) / elapsedSec : 0
  const netTxRate = prev ? Math.max(0, latest.net_tx_bytes - prev.net_tx_bytes) / elapsedSec : 0
  const blockRRate = prev ? Math.max(0, latest.block_read_bytes - prev.block_read_bytes) / elapsedSec : 0
  const blockWRate = prev ? Math.max(0, latest.block_write_bytes - prev.block_write_bytes) / elapsedSec : 0

  const statBox = (label: string, value: string, sub?: string, barPct?: number, barColor?: string) => (
    <div className="card" style={{
      padding: '0.75rem 1rem',
      minWidth: '150px',
      flex: '1 1 150px',
    }}>
      <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', letterSpacing: '0.04em', marginBottom: '0.4rem', fontWeight: 500 }}>
        {label.toUpperCase()}
      </div>
      <div style={{ fontFamily: MONO, fontSize: '1.25rem', fontWeight: 600, color: barColor ?? 'var(--fg-primary)' }}>
        {value}
      </div>
      {sub && (
        <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.2rem' }}>{sub}</div>
      )}
      {barPct !== undefined && (
        <div style={{ marginTop: '0.5rem', height: '3px', background: 'var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
          <div style={{ height: '100%', width: `${Math.min(100, barPct)}%`, background: barColor ?? 'var(--accent)', borderRadius: '6px', transition: 'width 0.4s ease' }} />
        </div>
      )}
    </div>
  )

  return (
    <div className="card" style={{ padding: '1rem 1.25rem', marginBottom: '0.5rem' }}>
      <div className="flex items-center gap-2 mb-3" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
        <Activity size={14} />
        {t('projectDetail.metrics.title')}
        <span style={{ marginLeft: 'auto', fontSize: '0.8125rem' }}>
          {t('projectDetail.metrics.updated', { time: new Date(latest.collected_at * 1000).toLocaleTimeString() })}
        </span>
      </div>
      <div className="flex flex-wrap gap-3">
        {statBox(
          t('projectDetail.metrics.cpu'),
          `${latest.cpu_percent.toFixed(1)}%`,
          undefined,
          latest.cpu_percent,
          cpuColor,
        )}
        {statBox(
          t('projectDetail.metrics.memory'),
          formatBytes(latest.mem_usage_bytes),
          latest.mem_limit_bytes > 0
            ? `/ ${formatBytes(latest.mem_limit_bytes)} (${memPct.toFixed(0)}%)`
            : undefined,
          memPct || undefined,
          memColor,
        )}
        {statBox(
          t('projectDetail.metrics.netRx'),
          `${formatBytes(netRxRate)}/s`,
          `${t('projectDetail.metrics.total')}: ${formatBytes(latest.net_rx_bytes)}`,
        )}
        {statBox(
          t('projectDetail.metrics.netTx'),
          `${formatBytes(netTxRate)}/s`,
          `${t('projectDetail.metrics.total')}: ${formatBytes(latest.net_tx_bytes)}`,
        )}
        {statBox(
          t('projectDetail.metrics.diskRead'),
          `${formatBytes(blockRRate)}/s`,
          `${t('projectDetail.metrics.total')}: ${formatBytes(latest.block_read_bytes)}`,
        )}
        {statBox(
          t('projectDetail.metrics.diskWrite'),
          `${formatBytes(blockWRate)}/s`,
          `${t('projectDetail.metrics.total')}: ${formatBytes(latest.block_write_bytes)}`,
        )}
      </div>
    </div>
  )
}

// ─── Traffic Panel ────────────────────────────────────────────────────────────

function TrafficPanel({ traffic }: { traffic: ProjectTraffic[] }) {
  const { t } = useTranslation()
  const hasRows = traffic.length > 0

  const last5m = traffic.filter(r => Date.now() / 1000 - r.observed_at < 300).length
  const last1h = traffic.filter(r => Date.now() / 1000 - r.observed_at < 3600).length
  const uniqueIPs = new Set(traffic.map(r => r.client_ip)).size
  const errRate = hasRows
    ? (traffic.filter(r => r.status >= 500).length / traffic.length) * 100
    : 0

  const statusColorFor = (s: number) => {
    if (s >= 500) return 'var(--danger)'
    if (s >= 400) return 'var(--warning)'
    if (s >= 300) return 'var(--fg-muted)'
    return 'var(--accent)'
  }

  return (
    <div style={{ marginTop: '1.5rem' }}>
      <div className="flex items-center gap-2 mb-3" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
        <Activity size={14} />
        {t('projectDetail.traffic.title', 'Traffic')}
        <span style={{ marginLeft: '1rem' }}>
          {t('projectDetail.traffic.last5m', '5m')}: {last5m}
        </span>
        <span>·</span>
        <span>{t('projectDetail.traffic.last1h', '1h')}: {last1h}</span>
        <span>·</span>
        <span>{t('projectDetail.traffic.uniqueIps', 'unique IPs')}: {uniqueIPs}</span>
        {errRate > 0 && (
          <>
            <span>·</span>
            <span style={{ color: 'var(--danger)' }}>
              5xx: {errRate.toFixed(1)}%
            </span>
          </>
        )}
      </div>
      {!hasRows ? (
        <div className="card" style={{
          padding: '1.5rem',
          textAlign: 'center',
          fontSize: '0.875rem',
          color: 'var(--fg-muted)',
        }}>
          {t('projectDetail.traffic.empty', 'No traffic recorded yet.')}
        </div>
      ) : (
        <div className="table-container" style={{
          maxHeight: '360px',
          overflowY: 'auto',
        }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.8125rem' }}>
            <thead>
              <tr>
                <th style={thStyle}>{t('projectDetail.traffic.time', 'TIME')}</th>
                <th style={thStyle}>{t('projectDetail.traffic.ip', 'CLIENT IP')}</th>
                <th style={thStyle}>{t('projectDetail.traffic.method', 'METHOD')}</th>
                <th style={{ ...thStyle, width: '100%' }}>{t('projectDetail.traffic.path', 'PATH')}</th>
                <th style={thStyle}>{t('projectDetail.traffic.status', 'STATUS')}</th>
                <th style={thStyle}>{t('projectDetail.traffic.duration', 'MS')}</th>
                <th style={thStyle}>{t('projectDetail.traffic.size', 'BYTES')}</th>
              </tr>
            </thead>
            <tbody>
              {traffic.map((r, i) => (
                <tr key={i}>
                  <td style={tdStyle} title={new Date(r.observed_at * 1000).toLocaleString()}>
                    {timeAgo(new Date(r.observed_at * 1000).toISOString())}
                  </td>
                  <td style={{ ...tdStyle, fontFamily: MONO }}>{r.client_ip}</td>
                  <td style={tdStyle}>{r.method}</td>
                  <td style={{ ...tdStyle, fontFamily: MONO, maxWidth: '400px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={r.path}>
                    {r.path}
                  </td>
                  <td style={{ ...tdStyle, color: statusColorFor(r.status), fontWeight: 600 }}>{r.status}</td>
                  <td style={tdStyle}>{r.duration_ms}</td>
                  <td style={tdStyle}>{formatBytes(r.bytes_sent)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

const thStyle: CSSProperties = {
  textAlign: 'left',
  padding: '0.5rem 0.75rem',
  color: 'var(--fg-muted)',
  fontSize: '0.8125rem',
  letterSpacing: '0.04em',
  fontWeight: 500,
  whiteSpace: 'nowrap',
}

const tdStyle: CSSProperties = {
  padding: '0.45rem 0.75rem',
  color: 'var(--fg-primary)',
  whiteSpace: 'nowrap',
  fontSize: '0.875rem',
}

function TunnelTrafficTab({ projectId }: { projectId: string }) {
  const [traffic, setTraffic] = useState<ProjectTraffic[]>([])

  useEffect(() => {
    api.projects.traffic(projectId, 100).then(setTraffic).catch(() => setTraffic([]))
    const iv = setInterval(() => {
      api.projects.traffic(projectId, 100).then(setTraffic).catch(() => {})
    }, 15_000)
    return () => clearInterval(iv)
  }, [projectId])

  return <TrafficPanel traffic={traffic} />
}

function ConfigTab({ form, onChange, onSave, onDelete, saving, saveError, isAdmin, onChangeOwner }: {
  form: Partial<Project>
  onChange: (f: Partial<Project>) => void
  onSave: () => void
  onDelete: () => void
  saving: boolean
  saveError?: string | null
  isAdmin: boolean
  onChangeOwner: (newOwnerId: string) => Promise<void>
}) {
  const { t } = useTranslation()
  const [users, setUsers] = useState<User[] | null>(null)
  const [nodes, setNodes] = useState<DeployNode[] | null>(null)
  const [ownerSaving, setOwnerSaving] = useState(false)
  const [ownerError, setOwnerError] = useState<string | null>(null)

  useEffect(() => {
    if (!isAdmin) return
    api.users.list().then(setUsers).catch(() => setUsers([]))
    api.nodes.list().then(ns => setNodes(ns.filter(n => n.role === 'deploy'))).catch(() => setNodes([]))
  }, [isAdmin])

  const handleOwnerSelect = async (newOwnerId: string) => {
    if (!newOwnerId || newOwnerId === form.owner_id) return
    setOwnerSaving(true)
    setOwnerError(null)
    try {
      await onChangeOwner(newOwnerId)
    } catch (err) {
      setOwnerError((err as Error).message)
    } finally {
      setOwnerSaving(false)
    }
  }
  const field = (label: string, key: keyof Project, hint?: string) => (
    <div key={key}>
      <label className="form-label">
        {label.toUpperCase()}
      </label>
      <input
        type="text"
        value={(form[key] ?? '') as string}
        onChange={e => onChange({ ...form, [key]: e.target.value })}
        className="form-input w-full"
        style={{ fontFamily: MONO }}
      />
      {hint && (
        <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
          {hint}
        </p>
      )}
    </div>
  )

  const nameIsValidPrefix = isValidDomainPrefix(form.name ?? '')
  const domainPrefixRequired = !nameIsValidPrefix
  const isCompose = form.project_type === 'compose'
  const isImage = form.project_type === 'image'
  const isTunnelType = form.project_type === 'domain_only'

  const [openSections, setOpenSections] = useState<Set<string>>(new Set(['identity']))
  const toggleSection = (id: string) =>
    setOpenSections(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  const renderSection = (id: string, title: string, body: React.ReactNode) => {
    const open = openSections.has(id)
    return (
      <div style={{ borderTop: '1px solid var(--border)' }}>
        <button
          type="button"
          onClick={() => toggleSection(id)}
          style={{ width: '100%', display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0.875rem 0', background: 'none', border: 'none', cursor: 'pointer' }}
        >
          <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{title}</span>
          {open ? <ChevronUp size={14} style={{ color: 'var(--fg-muted)' }} /> : <ChevronDown size={14} style={{ color: 'var(--fg-muted)' }} />}
        </button>
        {open && <div className="space-y-5" style={{ paddingBottom: '1rem' }}>{body}</div>}
      </div>
    )
  }

  return (
    <div className="max-w-lg">
      {renderSection('identity', t('projectDetail.config.sections.identity'), (
        <>
          {field(t('projectDetail.config.projectName'), 'name')}
          {field(t('projectDetail.config.description'), 'description')}
          {field(t('projectDetail.config.tags'), 'tags', t('projectDetail.config.tagsHint'))}
          <div>
            <label className="form-label">
              {t('projectDetail.config.icon').toUpperCase()}
            </label>
            <textarea
              value={(form.icon ?? '') as string}
              onChange={e => onChange({ ...form, icon: e.target.value })}
              rows={3}
              className="form-input w-full"
              style={{ fontFamily: MONO, resize: 'vertical' }}
              placeholder={t('projectDetail.config.iconPlaceholder')}
            />
            <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('projectDetail.config.iconHint')}
            </p>
          </div>
          <div>
            <label className="form-label">
              {t('projectDetail.config.domainPrefix').toUpperCase()}
              {domainPrefixRequired && (
                <span style={{ color: 'var(--danger)', marginLeft: '0.3em' }}>*</span>
              )}
            </label>
            <input
              type="text"
              value={(form.domain_prefix ?? '') as string}
              onChange={e => onChange({ ...form, domain_prefix: e.target.value })}
              placeholder={nameIsValidPrefix ? form.name : undefined}
              className="form-input w-full"
              style={{ fontFamily: MONO }}
            />
            <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: domainPrefixRequired ? 'var(--danger)' : 'var(--fg-muted)' }}>
              {nameIsValidPrefix
                ? t('projectDetail.config.domainOptional', { name: form.name })
                : t('projectDetail.config.domainRequired')}
            </p>
          </div>
        </>
      ))}

      {form.id && renderSection('aliases', 'Custom domains', (
        <ProjectAliasesPanel projectId={form.id} />
      ))}

      {!isTunnelType && renderSection('source', t('projectDetail.config.sections.source'), (
        <>
          {!isImage && field(t('projectDetail.config.gitUrl'), 'git_url')}
          {!isImage && field(t('projectDetail.config.gitBranch'), 'git_branch')}
          {isImage && field(t('projectDetail.config.imageRef'), 'image_ref', t('projectDetail.config.imageRefHint'))}
          {isImage && (
            <div>
              <label className="form-label">{t('projectDetail.config.containerPort').toUpperCase()}</label>
              <input
                type="number"
                min={1}
                max={65535}
                value={form.container_port ?? ''}
                onChange={e => onChange({ ...form, container_port: e.target.value === '' ? undefined : Number(e.target.value) })}
                className="form-input w-full"
                style={{ fontFamily: MONO }}
              />
              <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                {t('projectDetail.config.containerPortHint')}
              </p>
            </div>
          )}
          {isCompose && field(t('projectDetail.config.composeFilePath'), 'compose_file_path', t('projectDetail.config.composeFilePathHint'))}
          {isCompose && field(t('projectDetail.config.exposeService'), 'expose_service', t('projectDetail.config.exposeServiceHint'))}
          {isCompose && (
            <div>
              <label className="form-label">{t('projectDetail.config.exposePort').toUpperCase()}</label>
              <input
                type="number"
                min={1}
                max={65535}
                value={form.expose_port ?? ''}
                onChange={e => onChange({ ...form, expose_port: e.target.value === '' ? undefined : Number(e.target.value) })}
                className="form-input w-full"
                style={{ fontFamily: MONO }}
              />
              <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                {t('projectDetail.config.exposePortHint')}
              </p>
            </div>
          )}
          {!isCompose && !isImage && field(t('projectDetail.config.dockerfilePath'), 'dockerfile_path')}
          {isCompose && (
            <div>
              <label className="form-label">{t('projectDetail.config.pinnedNode').toUpperCase()}</label>
              <p style={{ fontFamily: MONO, fontSize: '0.875rem', color: form.pinned_node_id ? 'var(--fg-primary)' : 'var(--fg-muted)' }}>
                {form.pinned_node_id || t('projectDetail.config.pinnedNodeNone')}
              </p>
              <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                {t('projectDetail.config.pinnedNodeHint')}
              </p>
            </div>
          )}
          <div>
            <label className="form-label">
              {t('projectDetail.config.autoDeploy').toUpperCase()}
            </label>
            <label className="flex items-center gap-3 cursor-pointer">
              <div
                onClick={() => onChange({ ...form, auto_deploy_enabled: !form.auto_deploy_enabled })}
                className="relative rounded-full transition-colors duration-200"
                style={{
                  width: '36px', height: '20px',
                  background: form.auto_deploy_enabled ? 'var(--accent)' : 'var(--border)',
                  cursor: 'pointer',
                }}
              >
                <div
                  className="absolute top-1 rounded-full transition-transform duration-200"
                  style={{
                    width: '12px', height: '12px',
                    background: '#ffffff',
                    left: form.auto_deploy_enabled ? '18px' : '4px',
                  }}
                />
              </div>
              <span style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                {form.auto_deploy_enabled ? t('projectDetail.config.enabled') : t('projectDetail.config.disabled')}
              </span>
            </label>
            <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('projectDetail.config.autoDeployHint')}
            </p>
            {form.auto_deploy_enabled && (
              <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)', fontFamily: MONO }}>
                {t('projectDetail.config.autoDeployLastSha')}:{' '}
                {form.last_tracked_commit_sha
                  ? form.last_tracked_commit_sha.slice(0, 12)
                  : t('projectDetail.config.autoDeployNever')}
              </p>
            )}
          </div>
        </>
      ))}

      {!isCompose && !isTunnelType && renderSection('runtime', t('projectDetail.config.sections.runtime'), (
        <>
          {field(t('projectDetail.config.memoryLimit'), 'memory_limit', t('projectDetail.config.memoryLimitHint'))}
          {field(t('projectDetail.config.volumeMountPath'), 'volume_mount_path', t('projectDetail.config.volumeMountPathHint'))}
        </>
      ))}

      {renderSection('admin', t('projectDetail.config.sections.admin'), (
        <>
          <div>
            <label className="form-label">
              {t('projectDetail.config.owner').toUpperCase()}
            </label>
            {isAdmin ? (
              <>
                <OwnerCombobox
                  users={users}
                  currentOwnerId={form.owner_id}
                  currentOwnerLabel={
                    form.owner_name
                      ? `${form.owner_name}${form.owner_email ? ` (${form.owner_email})` : ''}`
                      : (form.owner_email || form.owner_id || '')
                  }
                  disabled={ownerSaving}
                  onSelect={handleOwnerSelect}
                />
                <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                  {ownerSaving
                    ? t('projectDetail.config.ownerSaving')
                    : t('projectDetail.config.ownerHint')}
                </p>
                {ownerError && (
                  <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--danger)' }}>{ownerError}</p>
                )}
              </>
            ) : (
              <p style={{ fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)' }}>
                {form.owner_name ? `${form.owner_name}${form.owner_email ? ` (${form.owner_email})` : ''}` : (form.owner_email || form.owner_id)}
              </p>
            )}
          </div>

          {!isTunnelType && (isAdmin || form.fixed_host_port) && (
            <div>
              <label className="form-label">
                {t('projectDetail.config.fixedPort').toUpperCase()}
              </label>
              {isAdmin ? (
                <div className="flex flex-col gap-2">
                  <input
                    type="number"
                    min={1024}
                    max={65535}
                    placeholder={t('projectDetail.config.fixedPortNone')}
                    value={form.fixed_host_port ?? ''}
                    onChange={e => onChange({ ...form, fixed_host_port: e.target.value === '' ? null : Number(e.target.value) })}
                    className="form-input w-full"
                    style={{ fontFamily: MONO }}
                  />
                  <select
                    value={form.fixed_node_id ?? ''}
                    onChange={e => onChange({ ...form, fixed_node_id: e.target.value || null })}
                    className="form-input w-full"
                    style={{ fontFamily: MONO }}
                  >
                    <option value="">{t('projectDetail.config.fixedNode')} —</option>
                    {(nodes ?? []).map(n => (
                      <option key={n.id} value={n.id}>{n.hostname} ({n.id.slice(0, 8)})</option>
                    ))}
                  </select>
                  {(form.fixed_host_port || form.fixed_node_id) && (
                    <button
                      type="button"
                      className="btn btn-ghost"
                      style={{ alignSelf: 'flex-start' }}
                      onClick={() => onChange({ ...form, fixed_host_port: null, fixed_node_id: null })}
                    >
                      {t('projectDetail.config.fixedPortClear')}
                    </button>
                  )}
                </div>
              ) : (
                <p style={{ fontFamily: MONO, fontSize: '0.875rem', color: form.fixed_host_port ? 'var(--fg-primary)' : 'var(--fg-muted)' }}>
                  {form.fixed_host_port ? `:${form.fixed_host_port} on ${form.fixed_node_id?.slice(0, 8) ?? '?'}` : t('projectDetail.config.fixedPortNone')}
                </p>
              )}
              <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                {t('projectDetail.config.fixedPortHint')}
              </p>
            </div>
          )}
        </>
      ))}

      <div style={{ borderTop: '1px solid var(--border)', marginTop: '0.25rem', paddingTop: '1rem' }}>
        {saveError && (
          <p style={{ fontSize: '0.875rem', color: 'var(--danger)', marginBottom: '0.75rem' }}>{saveError}</p>
        )}
        <div className="flex gap-3">
          <button
            onClick={onSave}
            disabled={saving}
            className="btn-primary"
            style={saving ? { cursor: 'not-allowed', opacity: 0.7 } : {}}
          >
            {saving ? t('projectDetail.config.saving') : t('projectDetail.config.saveChanges')}
          </button>
          <button
            onClick={onDelete}
            className="btn-danger flex items-center gap-2"
          >
            <Trash2 size={13} /> {t('projectDetail.config.delete')}
          </button>
        </div>
      </div>
    </div>
  )
}

function BrandingTab({ form, onChange, onSave, saving, saveError }: {
  form: Partial<Project>
  onChange: (f: Partial<Project>) => void
  onSave: () => void
  saving: boolean
  saveError?: string | null
}) {
  const { t } = useTranslation()
  const textField = (label: string, hint: string, key: keyof Project) => (
    <div>
      <label className="form-label">{label.toUpperCase()}</label>
      <input
        type="text"
        value={(form[key] ?? '') as string}
        onChange={e => onChange({ ...form, [key]: e.target.value })}
        className="form-input w-full"
        style={{ fontFamily: MONO }}
      />
      <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
        {hint}
      </p>
    </div>
  )
  const colorField = (label: string, hint: string, key: keyof Project) => {
    const raw = ((form[key] ?? '') as string).trim()
    const isHex = /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(raw)
    return (
      <div>
        <label className="form-label">{label.toUpperCase()}</label>
        <div className="flex items-center gap-2">
          <input
            type="color"
            value={isHex ? raw.slice(0, 7) : '#000000'}
            onChange={e => onChange({ ...form, [key]: e.target.value })}
            style={{ width: '40px', height: '36px', padding: 0, border: '1px solid var(--border)', borderRadius: '6px', background: 'transparent' }}
            aria-label={label}
          />
          <input
            type="text"
            value={raw}
            placeholder="#4f46e5"
            onChange={e => onChange({ ...form, [key]: e.target.value })}
            className="form-input flex-1"
            style={{ fontFamily: MONO }}
          />
        </div>
        <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
          {hint}
        </p>
      </div>
    )
  }
  return (
    <div className="space-y-4">
      <div>
        <h3 style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', margin: 0 }}>
          {t('projectDetail.config.brandingHeading')}
        </h3>
        <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
          {t('projectDetail.config.brandingHint')}
        </p>
      </div>
      {textField(t('projectDetail.config.brandingSiteName'), t('projectDetail.config.brandingSiteNameHint'), 'branding_site_name')}
      {textField(t('projectDetail.config.brandingLogoUrl'), t('projectDetail.config.brandingLogoUrlHint'), 'branding_logo_url')}
      {textField(t('projectDetail.config.brandingFaviconUrl'), t('projectDetail.config.brandingFaviconUrlHint'), 'branding_favicon_url')}
      {colorField(t('projectDetail.config.brandingPrimaryColor'), t('projectDetail.config.brandingPrimaryColorHint'), 'branding_primary_color')}
      {colorField(t('projectDetail.config.brandingSidebarBg'), t('projectDetail.config.brandingSidebarBgHint'), 'branding_sidebar_bg')}
      {textField(t('projectDetail.config.brandingTagline'), t('projectDetail.config.brandingTaglineHint'), 'branding_tagline')}
      <div>
        <label className="form-label">{t('projectDetail.config.brandingDescription').toUpperCase()}</label>
        <textarea
          value={(form.branding_description ?? '') as string}
          onChange={e => onChange({ ...form, branding_description: e.target.value })}
          rows={3}
          className="form-input w-full"
          style={{ resize: 'vertical' }}
        />
        <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
          {t('projectDetail.config.brandingDescriptionHint')}
        </p>
      </div>
      {textField(t('projectDetail.config.brandingFooterText'), t('projectDetail.config.brandingFooterTextHint'), 'branding_footer_text')}
      {textField(t('projectDetail.config.brandingTrustText'), t('projectDetail.config.brandingTrustTextHint'), 'branding_trust_text')}
      {saveError && (
        <p style={{ fontSize: '0.875rem', color: 'var(--danger)' }}>{saveError}</p>
      )}
      <div className="flex gap-3 pt-2">
        <button
          onClick={onSave}
          disabled={saving}
          className="btn-primary"
          style={saving ? { cursor: 'not-allowed', opacity: 0.7 } : {}}
        >
          {saving ? t('projectDetail.config.saving') : t('projectDetail.config.saveChanges')}
        </button>
      </div>
    </div>
  )
}

function OwnerCombobox({ users, currentOwnerId, currentOwnerLabel, disabled, onSelect }: {
  users: User[] | null
  currentOwnerId?: string
  currentOwnerLabel: string
  disabled: boolean
  onSelect: (userId: string) => void
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [highlight, setHighlight] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
        setQuery('')
      }
    }
    document.addEventListener('mousedown', onClickOutside)
    return () => document.removeEventListener('mousedown', onClickOutside)
  }, [open])

  const filtered = useMemo(() => {
    if (!users) return []
    const q = query.trim().toLowerCase()
    if (!q) return users
    return users.filter(u =>
      (u.name ?? '').toLowerCase().includes(q) ||
      (u.email ?? '').toLowerCase().includes(q)
    )
  }, [users, query])

  useEffect(() => { setHighlight(0) }, [query, open])

  const choose = (u: User) => {
    setOpen(false)
    setQuery('')
    onSelect(u.id)
  }

  const displayValue = open ? query : currentOwnerLabel

  return (
    <div ref={containerRef} style={{ position: 'relative' }}>
      <input
        type="text"
        value={displayValue}
        onFocus={() => { setOpen(true); setQuery('') }}
        onChange={e => { setQuery(e.target.value); setOpen(true) }}
        onKeyDown={e => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
            setHighlight(h => Math.min(h + 1, Math.max(filtered.length - 1, 0)))
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setHighlight(h => Math.max(h - 1, 0))
          } else if (e.key === 'Enter') {
            e.preventDefault()
            const u = filtered[highlight]
            if (u) choose(u)
          } else if (e.key === 'Escape') {
            setOpen(false)
            setQuery('')
          }
        }}
        placeholder={currentOwnerLabel}
        disabled={disabled}
        className="form-input w-full"
        style={{ fontFamily: MONO }}
      />
      {open && users !== null && (
        <div
          style={{
            position: 'absolute',
            top: '100%',
            left: 0,
            right: 0,
            marginTop: '4px',
            background: 'var(--bg-card)',
            border: '1px solid var(--border)',
            borderRadius: '6px',
            maxHeight: '240px',
            overflowY: 'auto',
            zIndex: 10,
            boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
          }}
        >
          {filtered.length === 0 ? (
            <div style={{ padding: '0.75rem', fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
              {t('projectDetail.config.ownerNoMatches')}
            </div>
          ) : filtered.map((u, i) => (
            <div
              key={u.id}
              onMouseDown={e => { e.preventDefault(); choose(u) }}
              onMouseEnter={() => setHighlight(i)}
              style={{
                padding: '0.5rem 0.75rem',
                fontSize: '0.8125rem',
                fontFamily: MONO,
                background: i === highlight ? 'var(--bg-hover)' : 'transparent',
                cursor: 'pointer',
                color: u.id === currentOwnerId ? 'var(--accent)' : 'var(--fg-primary)',
              }}
            >
              {u.name ? `${u.name} (${u.email})` : u.email}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AuthTab({ form, onChange, onSave, saving, saveError }: {
  form: Partial<Project>
  onChange: (f: Partial<Project>) => void
  onSave: () => void
  saving: boolean
  saveError?: string | null
}) {
  const { t } = useTranslation()

  return (
    <div className="max-w-lg space-y-5">
      <div>
        <label className="form-label" style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
          {t('projectDetail.config.requireGoogleAuth')}
          <a
            href="https://hoveychen.github.io/muvee/docs/service-auth-integration"
            target="_blank"
            rel="noopener noreferrer"
            title={t('projectDetail.config.authDocsHint')}
            style={{ color: 'var(--fg-muted)', display: 'inline-flex', opacity: 0.6 }}
          >
            <HelpCircle size={12} />
          </a>
        </label>
        <label className="flex items-center gap-3 cursor-pointer">
          <div
            onClick={() => onChange({ ...form, auth_required: !form.auth_required })}
            className="relative rounded-full transition-colors duration-200"
            style={{
              width: '36px', height: '20px',
              background: form.auth_required ? 'var(--accent)' : 'var(--border)',
              cursor: 'pointer',
            }}
          >
            <div
              className="absolute top-1 rounded-full transition-transform duration-200"
              style={{
                width: '12px', height: '12px',
                background: '#ffffff',
                left: form.auth_required ? '18px' : '4px',
              }}
            />
          </div>
          <span style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
            {form.auth_required ? t('projectDetail.config.enabled') : t('projectDetail.config.disabled')}
          </span>
        </label>
      </div>

      {form.auth_required && (
        <>
          <div>
            <label className="form-label">
              {t('projectDetail.config.allowedDomains').toUpperCase()}
            </label>
            <input
              type="text"
              value={(form.auth_allowed_domains ?? '') as string}
              onChange={e => onChange({ ...form, auth_allowed_domains: e.target.value })}
              className="form-input w-full"
              style={{ fontFamily: MONO }}
            />
          </div>

          <div>
            <label className="form-label">
              {t('projectDetail.config.bypassPaths').toUpperCase()}
            </label>
            <textarea
              value={(form.auth_bypass_paths ?? '') as string}
              onChange={e => onChange({ ...form, auth_bypass_paths: e.target.value })}
              rows={5}
              className="form-input w-full"
              style={{ fontFamily: MONO, resize: 'vertical' }}
              placeholder={'/health\n/api/public/*'}
            />
            <p style={{ fontSize: '0.8125rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('projectDetail.config.bypassPathsHint')}
            </p>
          </div>
        </>
      )}

      <EnabledProvidersField
        value={(form.enabled_providers ?? '') as string}
        onChange={v => onChange({ ...form, enabled_providers: v })}
      />

      {saveError && (
        <p style={{ fontSize: '0.875rem', color: 'var(--danger)' }}>{saveError}</p>
      )}

      <div className="flex gap-3 pt-2">
        <button
          onClick={onSave}
          disabled={saving}
          className="btn-primary"
          style={saving ? { cursor: 'not-allowed', opacity: 0.7 } : {}}
        >
          {saving ? t('projectDetail.config.saving') : t('projectDetail.config.saveChanges')}
        </button>
      </div>
    </div>
  )
}

interface GlobalProvider {
  id: string
  display_name: string
}

function parseProviderSet(raw: string): Set<string> {
  const out = new Set<string>()
  for (const tok of raw.split(',')) {
    const t = tok.trim().toLowerCase()
    if (t) out.add(t)
  }
  return out
}

function EnabledProvidersField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [globals, setGlobals] = useState<GlobalProvider[] | null>(null)
  useEffect(() => {
    // /api/auth/downstream-providers includes env-side providers AND any
    // social providers admins enabled via /admin/settings. /api/auth/providers
    // is the env-only list used by the platform login page; that one does NOT
    // see Google/Discord/etc. configured at runtime, which is why the project
    // Auth tab needs the downstream-specific endpoint.
    fetch('/api/auth/downstream-providers')
      .then(r => r.json())
      .then((data: GlobalProvider[]) => setGlobals(data))
      .catch(() => setGlobals([]))
  }, [])

  if (globals === null) return null
  if (globals.length === 0) return null

  const selected = parseProviderSet(value)
  const isAll = selected.size === 0

  const toggle = (id: string) => {
    // Materialise the current set first — if value is empty (== all), expand
    // to the full list so the user can deselect individual entries without
    // first being forced to "select all" manually.
    const base = isAll ? new Set(globals.map(g => g.id)) : new Set(selected)
    if (base.has(id)) base.delete(id)
    else base.add(id)
    if (base.size === globals.length) {
      onChange('') // back to "inherit all" sentinel
      return
    }
    onChange(Array.from(base).sort().join(','))
  }

  return (
    <div>
      <label className="form-label">SIGN-IN PROVIDERS</label>
      <div className="flex flex-col gap-2" style={{ marginTop: '0.4rem' }}>
        {globals.map(g => {
          const checked = isAll || selected.has(g.id)
          return (
            <label key={g.id} className="flex items-center gap-2" style={{ cursor: 'pointer', fontSize: '0.9rem' }}>
              <input
                type="checkbox"
                checked={checked}
                onChange={() => toggle(g.id)}
              />
              <span>{g.display_name}</span>
              <span style={{ color: 'var(--fg-muted)', fontFamily: MONO, fontSize: '0.75rem' }}>{g.id}</span>
            </label>
          )
        })}
      </div>
      <p style={{ fontSize: '0.8125rem', marginTop: '0.4rem', color: 'var(--fg-muted)' }}>
        Checked = available to the downstream sign-in flow (SDK + ForwardAuth login page). Leaving every provider checked stores the empty inherit-all sentinel.
      </p>
    </div>
  )
}

function UsersTab({ projectId, form, onChange, onSave, saving, saveError, ownerName, ownerEmail, ownerAvatarURL, domainPrefix, baseDomain }: {
  projectId: string
  form: Partial<Project>
  onChange: (f: Partial<Project>) => void
  onSave: () => void
  saving: boolean
  saveError?: string | null
  ownerName: string
  ownerEmail: string
  ownerAvatarURL: string
  domainPrefix: string
  baseDomain: string
}) {
  const isPrivate = form.access_mode === 'private'

  return (
    <div className="max-w-lg space-y-5">
      <div>
        <label className="form-label">Service access</label>
        <div className="flex gap-2">
          {(['public', 'private'] as const).map(mode => (
            <button
              key={mode}
              type="button"
              onClick={() => onChange({ ...form, access_mode: mode })}
              className="btn-secondary"
              style={{
                background: form.access_mode === mode ? 'var(--accent)' : 'var(--bg-elevated)',
                color: form.access_mode === mode ? '#fff' : 'var(--fg)',
                borderColor: form.access_mode === mode ? 'var(--accent)' : 'var(--border)',
                fontSize: '0.875rem',
                padding: '0.4rem 0.9rem',
              }}
            >
              {mode === 'public' ? 'Public' : 'Private'}
            </button>
          ))}
        </div>
        <p style={{ fontSize: '0.8125rem', marginTop: '0.5rem', color: 'var(--fg-muted)', lineHeight: 1.5 }}>
          {isPrivate
            ? 'Only the project owner, system admins, and explicitly invited users can reach the deployed service.'
            : 'Any authenticated muvee user can reach the deployed service (default).'}
        </p>

        {saveError && (
          <p style={{ fontSize: '0.875rem', color: 'var(--danger)', marginTop: '0.5rem' }}>{saveError}</p>
        )}

        <div className="flex gap-3 pt-2" style={{ marginTop: '0.75rem' }}>
          <button
            onClick={onSave}
            disabled={saving}
            className="btn-primary"
            style={saving ? { cursor: 'not-allowed', opacity: 0.7, fontSize: '0.875rem', padding: '0.4rem 0.9rem' } : { fontSize: '0.875rem', padding: '0.4rem 0.9rem' }}
          >
            {saving ? 'Saving…' : 'Save access mode'}
          </button>
        </div>
      </div>

      <div style={{ borderTop: '1px solid var(--border)', paddingTop: '1.25rem' }}>
        <label className="form-label">Owner (always allowed)</label>
        <div style={{
          display: 'flex',
          alignItems: 'center',
          gap: '0.6rem',
          padding: '0.5rem 0.6rem',
          borderRadius: '6px',
          border: '1px solid var(--border)',
          background: 'var(--bg-elevated)',
        }}>
          {ownerAvatarURL
            ? <img src={ownerAvatarURL} alt="" style={{ width: 24, height: 24, borderRadius: '50%' }} />
            : <div style={{ width: 24, height: 24, borderRadius: '50%', background: 'var(--border)' }} />}
          <div style={{ minWidth: 0 }}>
            <div style={{ fontSize: '0.875rem', fontWeight: 500 }}>{ownerName || ownerEmail}</div>
            {ownerEmail && ownerEmail !== ownerName && (
              <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>{ownerEmail}</div>
            )}
          </div>
        </div>
      </div>

      <ProjectAccessUsersPanel projectId={projectId} disabled={!isPrivate} />
      <ProjectInvitationLinksPanel projectId={projectId} disabled={!isPrivate} domainPrefix={domainPrefix} baseDomain={baseDomain} />
      <PendingRequestsPanel projectId={projectId} />
      <RecentVisitorsPanel projectId={projectId} isPrivate={isPrivate} />
    </div>
  )
}

function ProjectAccessUsersPanel({ projectId, disabled }: { projectId: string; disabled?: boolean }) {
  const [users, setUsers] = useState<ProjectAccessUser[]>([])
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.projects.accessUsers(projectId).then(setUsers).catch(e => setError(e.message))
  }, [projectId])

  const handleAdd = async () => {
    const trimmed = email.trim()
    if (!trimmed) return
    setBusy(true)
    setError(null)
    try {
      const next = await api.projects.addAccessUser(projectId, trimmed)
      setUsers(next)
      setEmail('')
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const handleRemove = async (userId: string) => {
    setBusy(true)
    setError(null)
    try {
      await api.projects.removeAccessUser(projectId, userId)
      setUsers(prev => prev.filter(u => u.user_id !== userId))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: '1.25rem' }}>
      <label className="form-label">Allowed users</label>
      <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.5 }}>
        The project owner and system admins always have access — no need to list them here.
        Users must have signed in to muvee at least once before they can be added.
        {disabled && ' This list takes effect only when service access is set to Private.'}
      </p>

      <div className="flex gap-2" style={{ marginBottom: '0.75rem' }}>
        <input
          type="email"
          value={email}
          onChange={e => setEmail(e.target.value)}
          placeholder="user@example.com"
          className="form-input"
          style={{ flex: 1 }}
          onKeyDown={e => { if (e.key === 'Enter') handleAdd() }}
          disabled={busy}
        />
        <button
          type="button"
          onClick={handleAdd}
          disabled={busy || !email.trim()}
          className="btn-primary"
          style={{ fontSize: '0.875rem', padding: '0.4rem 0.9rem' }}
        >
          Invite
        </button>
      </div>

      {error && (
        <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginBottom: '0.5rem' }}>{error}</p>
      )}

      {users.length === 0 ? (
        <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', fontStyle: 'italic' }}>
          No additional users yet.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {users.map(u => (
            <li key={u.user_id} style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '0.5rem 0.6rem',
              borderRadius: '6px',
              border: '1px solid var(--border)',
              marginBottom: '0.35rem',
              background: 'var(--bg-elevated)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', minWidth: 0 }}>
                {u.avatar_url
                  ? <img src={u.avatar_url} alt="" style={{ width: 24, height: 24, borderRadius: '50%' }} />
                  : <div style={{ width: 24, height: 24, borderRadius: '50%', background: 'var(--border)' }} />}
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: '0.875rem', fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {u.name || u.email}
                  </div>
                  <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {u.email}
                  </div>
                </div>
              </div>
              <button
                type="button"
                onClick={() => handleRemove(u.user_id)}
                className="btn-secondary"
                style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                disabled={busy}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function ProjectInvitationLinksPanel({ projectId, disabled, domainPrefix, baseDomain }: { projectId: string; disabled?: boolean; domainPrefix: string; baseDomain: string }) {
  const [links, setLinks] = useState<InvitationLink[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [showForm, setShowForm] = useState(false)
  const [expiresInDays, setExpiresInDays] = useState('')
  const [maxUses, setMaxUses] = useState('')
  const [freshLink, setFreshLink] = useState<{ id: string; url: string } | null>(null)
  const [copied, setCopied] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, InvitationLinkUse[] | 'loading' | undefined>>({})

  useEffect(() => {
    api.projects.invitationLinks(projectId).then(setLinks).catch(e => setError(e.message))
  }, [projectId])

  // Project-scoped invites should land on the project's own subdomain so the
  // visitor sees the project's branding + enabled_providers (rendered by
  // authservice /_oauth/login when accessed via the project subdomain) rather
  // than the platform's generic /login page. Fall back to the platform login
  // only when the project has not been assigned a domain prefix yet.
  const buildURL = (token: string) => {
    if (domainPrefix && baseDomain) {
      return `https://${domainPrefix}.${baseDomain}/?invite_token=${encodeURIComponent(token)}`
    }
    return `${window.location.origin}/login?invite_token=${encodeURIComponent(token)}`
  }

  const handleGenerate = async () => {
    setBusy(true)
    setError(null)
    try {
      const opts: { expires_in_days?: number; max_uses?: number } = {}
      const d = parseInt(expiresInDays, 10)
      if (!Number.isNaN(d) && d > 0) opts.expires_in_days = d
      const m = parseInt(maxUses, 10)
      if (!Number.isNaN(m) && m > 0) opts.max_uses = m
      const link = await api.projects.createInvitationLink(projectId, opts)
      if (link.token) {
        setFreshLink({ id: link.id, url: buildURL(link.token) })
      }
      setLinks(prev => [link, ...prev])
      setExpiresInDays('')
      setMaxUses('')
      setShowForm(false)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const handleRevoke = async (linkId: string) => {
    if (!confirm('Revoke this invitation link? It will no longer let new users in.')) return
    setBusy(true)
    setError(null)
    try {
      await api.projects.deleteInvitationLink(projectId, linkId)
      setLinks(prev => prev.filter(l => l.id !== linkId))
      if (freshLink && freshLink.id === linkId) setFreshLink(null)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const handleCopy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // ignore
    }
  }

  const toggleUses = async (linkId: string) => {
    if (expanded[linkId] && expanded[linkId] !== 'loading') {
      setExpanded(prev => ({ ...prev, [linkId]: undefined }))
      return
    }
    setExpanded(prev => ({ ...prev, [linkId]: 'loading' }))
    try {
      const uses = await api.projects.invitationLinkUses(projectId, linkId)
      setExpanded(prev => ({ ...prev, [linkId]: uses }))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
      setExpanded(prev => ({ ...prev, [linkId]: undefined }))
    }
  }

  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: '1.25rem' }}>
      <label className="form-label">Invitation links</label>
      <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.5 }}>
        Share a link so anyone (even users who haven't signed in to muvee yet) can join this project's allow-list by signing in once.
        {disabled && ' Links take effect only when service access is set to Private.'}
      </p>

      {freshLink && (
        <div style={{
          padding: '0.6rem 0.75rem',
          background: 'var(--bg-elevated)',
          border: '1px solid var(--accent)',
          borderRadius: '6px',
          marginBottom: '0.75rem',
        }}>
          <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginBottom: '0.35rem' }}>
            Copy this URL now — it will not be shown again.
          </div>
          <div className="flex gap-2" style={{ alignItems: 'center' }}>
            <input
              type="text"
              readOnly
              value={freshLink.url}
              className="form-input"
              style={{ flex: 1, fontSize: '0.8125rem', fontFamily: 'monospace' }}
              onFocus={e => e.currentTarget.select()}
            />
            <button
              type="button"
              onClick={() => handleCopy(freshLink.url)}
              className="btn-secondary"
              style={{ fontSize: '0.75rem', padding: '0.35rem 0.7rem' }}
            >
              {copied ? <><Check size={14} /> Copied</> : <><Copy size={14} /> Copy</>}
            </button>
          </div>
        </div>
      )}

      {!showForm ? (
        <button
          type="button"
          onClick={() => setShowForm(true)}
          className="btn-primary"
          style={{ fontSize: '0.875rem', padding: '0.4rem 0.9rem', marginBottom: '0.75rem' }}
          disabled={busy}
        >
          <Plus size={14} /> Generate link
        </button>
      ) : (
        <div style={{
          padding: '0.75rem',
          border: '1px solid var(--border)',
          borderRadius: '6px',
          marginBottom: '0.75rem',
          background: 'var(--bg-elevated)',
        }}>
          <div className="flex gap-2" style={{ marginBottom: '0.6rem', flexWrap: 'wrap' }}>
            <div style={{ flex: '1 1 180px', minWidth: 0 }}>
              <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>Expires in (days)</label>
              <input
                type="number"
                min="1"
                value={expiresInDays}
                onChange={e => setExpiresInDays(e.target.value)}
                placeholder="never"
                className="form-input"
                style={{ width: '100%', fontSize: '0.875rem' }}
              />
            </div>
            <div style={{ flex: '1 1 180px', minWidth: 0 }}>
              <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>Max uses</label>
              <input
                type="number"
                min="1"
                value={maxUses}
                onChange={e => setMaxUses(e.target.value)}
                placeholder="unlimited"
                className="form-input"
                style={{ width: '100%', fontSize: '0.875rem' }}
              />
            </div>
          </div>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={handleGenerate}
              className="btn-primary"
              style={{ fontSize: '0.875rem', padding: '0.4rem 0.9rem' }}
              disabled={busy}
            >
              Generate
            </button>
            <button
              type="button"
              onClick={() => { setShowForm(false); setExpiresInDays(''); setMaxUses('') }}
              className="btn-secondary"
              style={{ fontSize: '0.875rem', padding: '0.4rem 0.9rem' }}
              disabled={busy}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {error && (
        <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginBottom: '0.5rem' }}>{error}</p>
      )}

      {links.length === 0 ? (
        <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', fontStyle: 'italic' }}>
          No invitation links yet.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {links.map(link => {
            const useCount = link.use_count ?? 0
            const maxLabel = link.max_uses ? `${useCount} / ${link.max_uses}` : `${useCount} / ∞`
            const expiresLabel = link.expires_at
              ? new Date(link.expires_at).toLocaleString()
              : 'never'
            const expired = link.expires_at ? new Date(link.expires_at).getTime() < Date.now() : false
            const exhausted = link.max_uses != null && useCount >= link.max_uses
            const active = !expired && !exhausted
            const uses = expanded[link.id]
            return (
              <li key={link.id} style={{
                padding: '0.55rem 0.7rem',
                borderRadius: '6px',
                border: '1px solid var(--border)',
                marginBottom: '0.4rem',
                background: 'var(--bg-elevated)',
                opacity: active ? 1 : 0.65,
              }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '0.6rem' }}>
                  <div style={{ minWidth: 0, fontSize: '0.8125rem' }}>
                    <div>
                      <span style={{ fontWeight: 500 }}>Uses:</span> {maxLabel}
                      {!active && <span style={{ marginLeft: '0.6rem', color: 'var(--danger)', fontSize: '0.75rem' }}>
                        {expired ? 'expired' : 'exhausted'}
                      </span>}
                    </div>
                    <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.15rem' }}>
                      Expires: {expiresLabel} · Created {timeAgo(link.created_at)}
                      {link.invited_by_email && ` · by ${link.invited_by_email}`}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: '0.35rem' }}>
                    <button
                      type="button"
                      onClick={() => toggleUses(link.id)}
                      className="btn-secondary"
                      style={{ fontSize: '0.75rem', padding: '0.25rem 0.55rem' }}
                      disabled={useCount === 0}
                      title={useCount === 0 ? 'No uses yet' : 'Show who used this link'}
                    >
                      {uses && uses !== 'loading' ? 'Hide' : 'Users'} ({useCount})
                    </button>
                    <button
                      type="button"
                      onClick={() => handleRevoke(link.id)}
                      className="btn-secondary"
                      style={{ fontSize: '0.75rem', padding: '0.25rem 0.55rem' }}
                      disabled={busy}
                    >
                      Revoke
                    </button>
                  </div>
                </div>
                {uses === 'loading' && (
                  <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.5rem' }}>Loading…</div>
                )}
                {uses && uses !== 'loading' && uses.length > 0 && (
                  <ul style={{ listStyle: 'none', padding: 0, margin: '0.5rem 0 0 0' }}>
                    {uses.map(u => (
                      <li key={u.id} style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '0.5rem',
                        padding: '0.3rem 0.4rem',
                        fontSize: '0.75rem',
                        borderTop: '1px solid var(--border)',
                      }}>
                        {u.avatar_url
                          ? <img src={u.avatar_url} alt="" style={{ width: 18, height: 18, borderRadius: '50%' }} />
                          : <div style={{ width: 18, height: 18, borderRadius: '50%', background: 'var(--border)' }} />}
                        <span>{u.user_name || u.user_email || u.user_id}</span>
                        <span style={{ color: 'var(--fg-muted)', marginLeft: 'auto' }}>{timeAgo(u.used_at)}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function ProjectAliasesPanel({ projectId }: { projectId: string }) {
  const [aliases, setAliases] = useState<ProjectAlias[]>([])
  const [host, setHost] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [baseDomain, setBaseDomain] = useState('')
  const [publicIPs, setPublicIPs] = useState<string[]>([])
  const [project, setProject] = useState<Project | null>(null)
  const [showGuide, setShowGuide] = useState(false)
  const [showCloudflare, setShowCloudflare] = useState(false)
  const [copied, setCopied] = useState<string | null>(null)

  useEffect(() => {
    api.projects.aliases(projectId).then(setAliases).catch(e => setError(e.message))
    api.runtime.config().then(cfg => {
      setBaseDomain(cfg.base_domain || '')
      setPublicIPs(cfg.public_ips || [])
    }).catch(() => {})
    api.projects.get(projectId).then(setProject).catch(() => {})
  }, [projectId])

  const projectHost = project?.domain_prefix && baseDomain ? `${project.domain_prefix}.${baseDomain}` : ''
  const primaryIP = publicIPs.find(ip => ip.includes('.')) || publicIPs[0] || ''

  const handleAdd = async () => {
    const trimmed = host.trim().toLowerCase()
    if (!trimmed) return
    setBusy(true)
    setError(null)
    try {
      const created = await api.projects.addAlias(projectId, trimmed)
      setAliases(prev => [created, ...prev])
      setHost('')
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const handleRemove = async (aliasId: string) => {
    if (!confirm('Remove this custom domain? Visitors using it will get a connection error until DNS is repointed.')) return
    setBusy(true)
    setError(null)
    try {
      await api.projects.deleteAlias(projectId, aliasId)
      setAliases(prev => prev.filter(a => a.id !== aliasId))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const handleCopy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(label)
      setTimeout(() => setCopied(c => (c === label ? null : c)), 1500)
    } catch {
      // ignore
    }
  }

  const copyableValue = (label: string, value: string) => (
    <span style={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: '0.35rem',
      padding: '0.15rem 0.45rem',
      borderRadius: '4px',
      background: 'var(--bg-hover)',
      fontFamily: MONO,
      fontSize: '0.8125rem',
    }}>
      <code>{value}</code>
      <button
        type="button"
        onClick={() => handleCopy(label, value)}
        title="Copy to clipboard"
        style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0, color: 'var(--fg-muted)' }}
      >
        {copied === label ? <Check size={12} /> : <Copy size={12} />}
      </button>
    </span>
  )

  return (
    <div>
      <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.5 }}>
        Attach extra hostnames that route to this same project. Each host needs DNS pointing at this server, then a Let's Encrypt certificate is issued automatically on the first request after DNS resolves. Sign-in state is not shared between different hostnames — visitors sign in once per host.
      </p>

      <button
        type="button"
        onClick={() => setShowGuide(v => !v)}
        className="btn-secondary"
        style={{ fontSize: '0.8125rem', padding: '0.35rem 0.7rem', marginBottom: '0.75rem' }}
      >
        {showGuide ? <ChevronUp size={14} /> : <ChevronDown size={14} />} How to set up DNS for a new domain
      </button>

      {showGuide && (
        <div style={{
          padding: '0.85rem',
          border: '1px solid var(--border)',
          borderRadius: '6px',
          marginBottom: '0.85rem',
          background: 'var(--bg-elevated)',
          fontSize: '0.8125rem',
          lineHeight: 1.6,
        }}>
          <p style={{ fontWeight: 600, marginBottom: '0.4rem' }}>Subdomain (e.g. <code>app.example.com</code>) — easiest path</p>
          <p style={{ marginBottom: '0.4rem', color: 'var(--fg-muted)' }}>
            At your DNS provider, add a single CNAME record:
          </p>
          <ul style={{ margin: '0 0 0.6rem 1.2rem', padding: 0, color: 'var(--fg-muted)' }}>
            <li>Type: <code>CNAME</code></li>
            <li>Name / host: the part before your apex (e.g. <code>app</code> for <code>app.example.com</code>)</li>
            <li>Target / value: {projectHost ? copyableValue('cname-target', projectHost) : <code>{'<this project\'s built-in subdomain>'}</code>}</li>
            <li>TTL: leave default (Auto / 300s is fine)</li>
          </ul>

          <p style={{ fontWeight: 600, margin: '0.85rem 0 0.4rem' }}>Apex / naked domain (e.g. <code>example.com</code> with nothing before it)</p>
          <p style={{ marginBottom: '0.4rem', color: 'var(--fg-muted)' }}>
            DNS forbids a CNAME on the apex, so use an A record pointing at the platform IP:
          </p>
          <ul style={{ margin: '0 0 0.6rem 1.2rem', padding: 0, color: 'var(--fg-muted)' }}>
            <li>Type: <code>A</code></li>
            <li>Name / host: <code>@</code> (i.e. the apex itself)</li>
            <li>Target / value: {primaryIP ? copyableValue('apex-a', primaryIP) : <code>{'<resolving…>'}</code>}</li>
            {publicIPs.length > 1 && (
              <li style={{ fontStyle: 'italic' }}>
                The platform has multiple addresses ({publicIPs.join(', ')}). Any one works; add one A record per address if you want redundancy.
              </li>
            )}
          </ul>
          <p style={{ color: 'var(--fg-muted)', marginBottom: '0.4rem' }}>
            Some DNS providers offer an <code>ALIAS</code> or <code>ANAME</code> record type at the apex — if yours does, you can use it instead and point it at {projectHost ? <code>{projectHost}</code> : <code>{'<this project\'s built-in subdomain>'}</code>}; the provider will automatically follow the CNAME chain for you.
          </p>

          <p style={{ fontWeight: 600, margin: '0.85rem 0 0.4rem' }}>After saving the DNS record</p>
          <ul style={{ margin: 0, padding: '0 0 0 1.2rem', color: 'var(--fg-muted)' }}>
            <li>Add the host below — Traefik picks it up within ~5 seconds.</li>
            <li>The first HTTPS request triggers a Let's Encrypt HTTP-01 challenge; expect a few seconds of "your connection is not private" while the cert is being issued, then it sticks.</li>
            <li>If you hit a TLS error that persists past ~30 seconds, the most common cause is that DNS hasn't propagated yet. Check with <code>dig +short {'<your-host>'}</code>; it should return {primaryIP ? <code>{primaryIP}</code> : 'the IP above'} or this project's CNAME target.</li>
          </ul>
        </div>
      )}

      <button
        type="button"
        onClick={() => setShowCloudflare(v => !v)}
        className="btn-secondary"
        style={{ fontSize: '0.8125rem', padding: '0.35rem 0.7rem', marginBottom: '0.75rem' }}
      >
        {showCloudflare ? <ChevronUp size={14} /> : <ChevronDown size={14} />} Cloudflare quickstart
      </button>

      {showCloudflare && (
        <div style={{
          padding: '0.85rem',
          border: '1px solid var(--border)',
          borderRadius: '6px',
          marginBottom: '0.85rem',
          background: 'var(--bg-elevated)',
          fontSize: '0.8125rem',
          lineHeight: 1.6,
        }}>
          <p style={{ marginBottom: '0.4rem' }}>
            Cloudflare supports CNAME flattening, so you can use one record type for both subdomains and the apex. Steps inside the Cloudflare dashboard:
          </p>
          <ol style={{ margin: '0 0 0.6rem 1.2rem', padding: 0, color: 'var(--fg-muted)' }}>
            <li>Open <strong>DNS → Records</strong> for your zone.</li>
            <li>Click <strong>Add record</strong>. Type: <code>CNAME</code>, Name: <code>@</code> for apex or e.g. <code>app</code> for a subdomain, Target: {projectHost ? copyableValue('cf-target', projectHost) : <code>{'<this project\'s built-in subdomain>'}</code>}.</li>
            <li>Proxy status: either is fine, but they have different trade-offs:
              <ul style={{ margin: '0.25rem 0 0.25rem 1.2rem', padding: 0 }}>
                <li><strong>DNS only</strong> (gray cloud) — simplest. The browser hits this platform directly with the Let's Encrypt cert we issue. Recommended unless you need Cloudflare's CDN/WAF.</li>
                <li><strong>Proxied</strong> (orange cloud) — Cloudflare terminates TLS with its own cert. Go to <strong>SSL/TLS → Overview</strong> and set the encryption mode to <strong>Full (strict)</strong> so Cloudflare validates the origin cert against the public CA chain. <code>Flexible</code> will cause redirect loops; <code>Full</code> (non-strict) leaves the origin link unverified.</li>
              </ul>
            </li>
            <li>Save. Cloudflare's edge picks the record up within seconds.</li>
          </ol>
          <p style={{ color: 'var(--fg-muted)' }}>
            If you turn on the orange-cloud proxy <em>and</em> use <strong>Always Use HTTPS</strong> at Cloudflare, leave Traefik's HTTP→HTTPS redirect intact — both layers redirect cleanly. If you also enable <strong>HSTS</strong> at Cloudflare, do it after you've confirmed certs are working, since browsers will then refuse downgrades for the configured max-age.
          </p>
        </div>
      )}

      <div className="flex gap-2" style={{ marginBottom: '0.75rem', flexWrap: 'wrap' }}>
        <input
          type="text"
          value={host}
          onChange={e => setHost(e.target.value)}
          placeholder="app.example.com"
          className="form-input"
          style={{ flex: '1 1 220px', minWidth: 0, fontSize: '0.875rem', fontFamily: 'monospace' }}
          disabled={busy}
          onKeyDown={e => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void handleAdd()
            }
          }}
        />
        <button
          type="button"
          onClick={handleAdd}
          className="btn-primary"
          style={{ fontSize: '0.875rem', padding: '0.4rem 0.9rem' }}
          disabled={busy || host.trim() === ''}
        >
          <Plus size={14} /> Add domain
        </button>
      </div>

      {error && (
        <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginBottom: '0.5rem' }}>{error}</p>
      )}

      {aliases.length === 0 ? (
        <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', fontStyle: 'italic' }}>
          No custom domains yet.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {aliases.map(a => (
            <li key={a.id} style={{
              padding: '0.55rem 0.7rem',
              borderRadius: '6px',
              border: '1px solid var(--border)',
              marginBottom: '0.4rem',
              background: 'var(--bg-elevated)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: '0.5rem',
            }}>
              <a
                href={`https://${a.host}`}
                target="_blank"
                rel="noopener noreferrer"
                style={{ fontFamily: MONO, fontSize: '0.875rem', textDecoration: 'none', color: 'var(--fg-primary)' }}
              >
                {a.host}
              </a>
              <button
                type="button"
                onClick={() => handleRemove(a.id)}
                className="btn-secondary"
                style={{ fontSize: '0.75rem', padding: '0.3rem 0.6rem' }}
                disabled={busy}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function PendingRequestsPanel({ projectId }: { projectId: string }) {
  const [reqs, setReqs] = useState<ProjectAccessRequest[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [hidden, setHidden] = useState(false)

  const refresh = () => {
    api.projects.accessRequests(projectId, 'pending')
      .then(setReqs)
      .catch(e => {
        // 403 from non-owner non-admin viewers — silently hide the panel.
        if (/403|forbidden/i.test(e.message)) {
          setHidden(true)
        } else {
          setError(e.message)
        }
      })
  }
  useEffect(refresh, [projectId])

  const decide = async (id: string, decision: 'approve' | 'deny') => {
    setBusy(true)
    setError(null)
    try {
      if (decision === 'approve') await api.accessRequests.approve(id)
      else await api.accessRequests.deny(id)
      setReqs(prev => prev.filter(r => r.id !== id))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (hidden) return null
  if (reqs.length === 0 && !error) return null

  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: '1.25rem' }}>
      <label className="form-label">Pending access requests</label>
      <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.5 }}>
        Users who hit the deny page asked for access. Approve to add them to the allow-list immediately.
      </p>
      {error && <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginBottom: '0.5rem' }}>{error}</p>}
      <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
        {reqs.map(r => (
          <li key={r.id} style={{
            padding: '0.5rem 0.6rem',
            borderRadius: '6px',
            border: '1px solid var(--border)',
            marginBottom: '0.35rem',
            background: 'var(--bg-elevated)',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '0.5rem' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', minWidth: 0 }}>
                {r.user_avatar_url
                  ? <img src={r.user_avatar_url} alt="" style={{ width: 24, height: 24, borderRadius: '50%' }} />
                  : <div style={{ width: 24, height: 24, borderRadius: '50%', background: 'var(--border)' }} />}
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: '0.875rem', fontWeight: 500 }}>{r.user_name || r.user_email}</div>
                  <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>{r.user_email}</div>
                </div>
              </div>
              <div style={{ display: 'flex', gap: '0.4rem' }}>
                <button type="button" className="btn-primary" disabled={busy}
                  style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                  onClick={() => decide(r.id, 'approve')}>Approve</button>
                <button type="button" className="btn-secondary" disabled={busy}
                  style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                  onClick={() => decide(r.id, 'deny')}>Deny</button>
              </div>
            </div>
            {r.reason && (
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.4rem', fontStyle: 'italic' }}>
                "{r.reason}"
              </p>
            )}
            <p style={{ fontSize: '0.7rem', color: 'var(--fg-muted)', marginTop: '0.2rem' }}>
              Requested {timeAgo(r.requested_at)}
            </p>
          </li>
        ))}
      </ul>
    </div>
  )
}

function RecentVisitorsPanel({ projectId, isPrivate }: { projectId: string; isPrivate: boolean }) {
  const [visits, setVisits] = useState<ProjectVisit[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const refresh = () => {
    api.projects.visits(projectId)
      .then(setVisits)
      .catch(e => setError(e.message))
  }
  useEffect(refresh, [projectId])

  const allow = async (userEmail: string, userId: string) => {
    setBusy(true)
    setError(null)
    try {
      await api.projects.addAccessUser(projectId, userEmail)
      setVisits(prev => prev.map(v => v.user_id === userId ? { ...v, in_allow_list: true } : v))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: '1.25rem' }}>
      <label className="form-label">Recent visitors</label>
      <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.5 }}>
        Unique users who reached the deployed service.
        {isPrivate && ' Add anyone here directly to the allow-list.'}
      </p>
      {error && <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginBottom: '0.5rem' }}>{error}</p>}
      {visits.length === 0 ? (
        <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', fontStyle: 'italic' }}>
          No visitors recorded yet.
        </p>
      ) : (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {visits.map(v => (
            <li key={v.user_id} style={{
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              padding: '0.5rem 0.6rem', borderRadius: '6px',
              border: '1px solid var(--border)', marginBottom: '0.35rem',
              background: 'var(--bg-elevated)',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', minWidth: 0 }}>
                {v.avatar_url
                  ? <img src={v.avatar_url} alt="" style={{ width: 24, height: 24, borderRadius: '50%' }} />
                  : <div style={{ width: 24, height: 24, borderRadius: '50%', background: 'var(--border)' }} />}
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: '0.875rem', fontWeight: 500 }}>{v.name || v.email}</div>
                  <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                    {v.email} · {v.visit_count} visits · last {timeAgo(v.last_seen_at)}
                  </div>
                </div>
              </div>
              {isPrivate && (
                v.in_allow_list ? (
                  <span style={{ fontSize: '0.7rem', color: 'var(--fg-muted)' }}>Allowed</span>
                ) : (
                  <button type="button" className="btn-secondary" disabled={busy}
                    style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem' }}
                    onClick={() => allow(v.email, v.user_id)}>Allow</button>
                )
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function DatasetsTab({ available, selected, datasetBasePath, onToggle, onUpdateMode }: {
  available: Dataset[]
  selected: ProjectDataset[]
  datasetBasePath: string
  onToggle: (id: string, mode: 'dependency' | 'readwrite') => void
  onUpdateMode: (id: string, mode: 'dependency' | 'readwrite') => void
}) {
  const { t } = useTranslation()
  const selectedMap = Object.fromEntries(selected.map(pd => [pd.dataset_id, pd]))

  return (
    <div>
      <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', marginBottom: '1.5rem', lineHeight: 1.6 }}>
        {t('projectDetail.datasets.hint', {
          defaultValue: 'Select datasets to mount into the container. <accent>dependency</accent> = rsync copy (LRU cached). <blue>readwrite</blue> = direct NFS mount.',
        }).split('<accent>dependency</accent>').map((part, i, arr) =>
          i < arr.length - 1 ? (
            <span key={i}>
              {part}
              <span style={{ color: 'var(--accent)' }}>dependency</span>
            </span>
          ) : part.split('<blue>readwrite</blue>').map((p, j, a) =>
            j < a.length - 1 ? (
              <span key={j}>
                {p}
                <span style={{ color: 'var(--accent)' }}>readwrite</span>
              </span>
            ) : p
          )
        )}
      </p>
      <div className="card" style={{ overflow: 'hidden' }}>
        {available.map((ds, i) => {
          const sel = selectedMap[ds.id]
          return (
            <div
              key={ds.id}
              className="flex items-center gap-4 px-5 py-4"
              style={{ background: sel ? 'var(--bg-hover)' : 'var(--bg-card)', borderBottom: i < available.length - 1 ? '1px solid var(--border)' : 'none' }}
            >
              <input
                type="checkbox"
                checked={!!sel}
                onChange={() => onToggle(ds.id, 'dependency')}
                style={{ accentColor: 'var(--accent)', width: '14px', height: '14px' }}
              />
              <div className="flex-1 min-w-0">
                <div style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--fg-primary)' }}>{ds.name}</div>
                <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginTop: '2px' }}>
                  {resolveDatasetPath(datasetBasePath, ds.nfs_path)}
                </div>
              </div>
              <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
                {formatBytes(ds.size_bytes)} · v{ds.version}
              </div>
              {sel && (
                <select
                  value={sel.mount_mode}
                  onChange={e => onUpdateMode(ds.id, e.target.value as 'dependency' | 'readwrite')}
                  style={{
                    background: 'var(--bg-base)',
                    border: '1px solid var(--border)',
                    color: 'var(--accent)',
                    fontFamily: MONO,
                    fontSize: '0.8125rem',
                    padding: '4px 8px',
                    borderRadius: '6px',
                    cursor: 'pointer',
                    flexShrink: 0,
                  }}
                >
                  <option value="dependency">dependency</option>
                  <option value="readwrite">readwrite</option>
                </select>
              )}
            </div>
          )
        })}
        {available.length === 0 && (
          <div className="py-10 text-center" style={{ color: 'var(--fg-muted)', fontSize: '0.875rem', background: 'var(--bg-card)' }}>
            {t('projectDetail.datasets.empty')}
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Workspace Tab ────────────────────────────────────────────────────────────

function WorkspaceTab({ projectId, volumeMountPath }: { projectId: string; volumeMountPath: string }) {
  const { t } = useTranslation()
  const [path, setPath] = useState('')
  const [entries, setEntries] = useState<WorkspaceEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notConfigured, setNotConfigured] = useState(false)
  const [uploading, setUploading] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const load = async (p: string) => {
    setLoading(true)
    setError(null)
    try {
      const data = await api.projects.workspaceList(projectId, p)
      setEntries(data)
      setPath(p)
    } catch (e: unknown) {
      const msg = (e as Error).message
      if (msg.includes('503') || msg.includes('not configured')) {
        setNotConfigured(true)
      } else {
        setError(t('projectDetail.workspace.errorList') + ': ' + msg)
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load('') }, [projectId])

  if (!volumeMountPath) {
    return (
      <div style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', padding: '2rem 0' }}>
        {t('projectDetail.workspace.noVolume')}
      </div>
    )
  }
  if (notConfigured) {
    return (
      <div style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', padding: '2rem 0' }}>
        {t('projectDetail.workspace.notConfigured')}
      </div>
    )
  }

  const breadcrumbs = path ? path.split('/').filter(Boolean) : []

  const handleDelete = async (entry: WorkspaceEntry) => {
    const entryPath = path ? path + '/' + entry.name : entry.name
    if (!confirm(t('projectDetail.workspace.deleteConfirm', { name: entry.name }))) return
    try {
      await api.projects.workspaceDelete(projectId, entryPath)
      load(path)
    } catch (e: unknown) {
      alert(t('projectDetail.workspace.errorDelete') + ': ' + (e as Error).message)
    }
  }

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setUploading(true)
    try {
      await api.projects.workspaceUpload(projectId, path, file)
      load(path)
    } catch (err: unknown) {
      alert(t('projectDetail.workspace.errorUpload') + ': ' + (err as Error).message)
    } finally {
      setUploading(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }

  const navigateTo = (idx: number) => {
    const newPath = breadcrumbs.slice(0, idx + 1).join('/')
    load(newPath)
  }

  return (
    <div>
      {/* Breadcrumb */}
      <div className="flex items-center gap-1 mb-4" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
        <button
          onClick={() => load('')}
          style={{ background: 'none', border: 'none', cursor: 'pointer', color: path ? 'var(--accent)' : 'var(--fg-primary)', fontSize: '0.875rem', padding: 0, fontWeight: 500 }}
        >
          {t('projectDetail.workspace.root')}
        </button>
        {breadcrumbs.map((seg, i) => (
          <span key={i} className="flex items-center gap-1">
            <span style={{ color: 'var(--fg-muted)' }}>/</span>
            <button
              onClick={() => navigateTo(i)}
              style={{
                background: 'none', border: 'none', cursor: 'pointer',
                color: i === breadcrumbs.length - 1 ? 'var(--fg-primary)' : 'var(--accent)',
                fontSize: '0.875rem', padding: 0,
              }}
            >
              {seg}
            </button>
          </span>
        ))}
      </div>

      {/* Upload button */}
      <div className="flex items-center gap-3 mb-4">
        <input ref={fileInputRef} type="file" style={{ display: 'none' }} onChange={handleUpload} />
        <button
          onClick={() => fileInputRef.current?.click()}
          disabled={uploading}
          className="btn-secondary flex items-center gap-2"
          style={uploading ? { cursor: 'not-allowed' } : {}}
        >
          {uploading ? t('projectDetail.workspace.uploading') : t('projectDetail.workspace.upload')}
        </button>
      </div>

      {/* File list */}
      {error && (
        <div style={{ fontSize: '0.875rem', color: 'var(--danger)', marginBottom: '1rem' }}>{error}</div>
      )}
      {loading ? (
        <div style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>{t('projectDetail.workspace.loading')}</div>
      ) : entries.length === 0 ? (
        <div style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', padding: '2rem 0' }}>{t('projectDetail.workspace.empty')}</div>
      ) : (
        <div className="card" style={{ overflow: 'hidden' }}>
          {entries.map((entry, i) => {
            const entryPath = path ? path + '/' + entry.name : entry.name
            return (
              <div
                key={entry.name}
                className="flex items-center gap-4 px-5 py-3"
                style={{
                  background: 'var(--bg-card)',
                  borderBottom: i < entries.length - 1 ? '1px solid var(--border)' : 'none',
                }}
                onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.background = 'var(--bg-hover)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = 'var(--bg-card)' }}
              >
                {/* Icon */}
                <span style={{ color: entry.is_dir ? 'var(--accent)' : 'var(--fg-muted)', flexShrink: 0 }}>
                  {entry.is_dir ? <FolderOpen size={14} /> : <File size={14} />}
                </span>

                {/* Name */}
                <div className="flex-1 min-w-0">
                  {entry.is_dir ? (
                    <button
                      onClick={() => load(entryPath)}
                      style={{
                        background: 'none', border: 'none', cursor: 'pointer', padding: 0,
                        fontFamily: MONO, fontSize: '0.875rem', color: 'var(--accent)', textAlign: 'left',
                      }}
                    >
                      {entry.name}/
                    </button>
                  ) : (
                    <span style={{ fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)' }}>{entry.name}</span>
                  )}
                </div>

                {/* Size */}
                <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', flexShrink: 0, minWidth: '64px', textAlign: 'right' }}>
                  {entry.is_dir ? '—' : formatBytes(entry.size)}
                </span>

                {/* Modified time */}
                <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', flexShrink: 0, minWidth: '120px', textAlign: 'right' }}>
                  {timeAgo(new Date(entry.mod_time * 1000).toISOString())}
                </span>

                {/* Actions */}
                <div className="flex items-center gap-2" style={{ flexShrink: 0 }}>
                  {!entry.is_dir && (
                    <a
                      href={api.projects.workspaceDownloadUrl(projectId, entryPath)}
                      download={entry.name}
                      title={t('projectDetail.workspace.download')}
                      style={{ color: 'var(--fg-muted)', display: 'flex', alignItems: 'center' }}
                      onMouseEnter={e => { (e.currentTarget as HTMLAnchorElement).style.color = 'var(--accent)' }}
                      onMouseLeave={e => { (e.currentTarget as HTMLAnchorElement).style.color = 'var(--fg-muted)' }}
                    >
                      <Download size={13} />
                    </a>
                  )}
                  <button
                    onClick={() => handleDelete(entry)}
                    title={t('projectDetail.workspace.delete')}
                    style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', display: 'flex', alignItems: 'center', padding: 0 }}
                    onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--danger)' }}
                    onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ─── Secrets Tab ──────────────────────────────────────────────────────────────

function SecretsTab({
  projectId,
  allSecrets,
  bindings,
  onBindingsChange,
}: {
  projectId: string
  allSecrets: Secret[]
  bindings: ProjectSecretBinding[]
  onBindingsChange: (b: ProjectSecretBinding[]) => void
}) {
  const [saving, setSaving] = useState(false)
  const { t } = useTranslation()

  const isBound = (secretId: string) => bindings.some(b => b.secret_id === secretId)
  const getBinding = (secretId: string) => bindings.find(b => b.secret_id === secretId)

  const save = async (updated: ProjectSecretBinding[]) => {
    setSaving(true)
    try {
      await api.projects.setSecrets(
        projectId,
        updated.map(b => ({
          secret_id: b.secret_id,
          env_var_name: b.env_var_name,
          use_for_git: b.use_for_git,
          use_for_build: b.use_for_build,
          build_secret_id: b.build_secret_id,
          git_username: b.git_username,
        })),
      )
      onBindingsChange(updated)
    } catch (e) {
      alert(t('projectDetail.secrets.failedToUpdate') + (e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const toggleBind = (sec: Secret) => {
    if (isBound(sec.id)) {
      save(bindings.filter(b => b.secret_id !== sec.id))
    } else {
      save([
        ...bindings,
        {
          secret_id: sec.id,
          secret_name: sec.name,
          secret_type: sec.type,
          env_var_name: sec.name.toUpperCase().replace(/[^A-Z0-9]/g, '_'),
          use_for_git: false,
          use_for_build: false,
          build_secret_id: sec.name.toLowerCase().replace(/[^a-z0-9_]/g, '_'),
          git_username: sec.type === 'password' ? 'x-access-token' : '',
        },
      ])
    }
  }

  const updateField = (secretId: string, patch: Partial<ProjectSecretBinding>) => {
    save(bindings.map(b => b.secret_id === secretId ? { ...b, ...patch } : b))
  }

  return (
    <div>
      <div className="mb-4" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
        {t('projectDetail.secrets.hint')}
        {saving && <span style={{ marginLeft: '1em', color: 'var(--accent)' }}>{t('projectDetail.secrets.saving')}</span>}
      </div>

      <div className="card" style={{ overflow: 'hidden' }}>
        {allSecrets.length === 0 ? (
          <div className="py-12 text-center" style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
            {t('projectDetail.secrets.empty')}{' '}
            <a href="/secrets" style={{ color: 'var(--accent)', textDecoration: 'none' }}>{t('projectDetail.secrets.emptyLink')}</a>
            {' '}{t('projectDetail.secrets.emptySuffix')}
          </div>
        ) : (
          allSecrets.map((sec, i) => {
            const bound = isBound(sec.id)
            const binding = getBinding(sec.id)
            return (
              <div
                key={sec.id}
                style={{
                  borderBottom: i < allSecrets.length - 1 ? '1px solid var(--border)' : 'none',
                  padding: '1rem 1.25rem',
                  transition: 'background 0.1s',
                }}
                onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.background = 'var(--bg-hover)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = 'transparent' }}
              >
                <div className="flex items-start gap-3">
                  {/* Bind toggle */}
                  <button
                    onClick={() => toggleBind(sec)}
                    title={bound ? t('projectDetail.secrets.detach') : t('projectDetail.secrets.attach')}
                    style={{ background: 'none', border: 'none', cursor: 'pointer', color: bound ? 'var(--accent)' : 'var(--fg-muted)', marginTop: '2px', flexShrink: 0 }}
                  >
                    {bound ? <Link2 size={15} /> : <Link2Off size={15} />}
                  </button>

                  <div className="flex-1">
                    {/* Name & type badge */}
                    <div className="flex items-center gap-2 mb-1">
                      <span style={{ fontSize: '0.875rem', color: 'var(--fg-primary)', fontWeight: 500 }}>
                        {sec.name}
                      </span>
                      <span className={`badge ${
                        sec.type === 'ssh_key' ? 'badge-info' :
                        sec.type === 'api_key' ? 'badge-warning' :
                        sec.type === 'env_var' ? 'badge-success' :
                        'badge-neutral'
                      }`}>
                        {sec.type === 'ssh_key' ? t('secrets.sshKey') :
                         sec.type === 'api_key' ? t('secrets.apiKey') :
                         sec.type === 'env_var' ? t('secrets.envVar') :
                         t('secrets.password')}
                      </span>
                    </div>

                    {/* Binding options — only shown when bound */}
                    {bound && binding && (
                      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 mt-2">

                        {/* Env var name (all types) */}
                        <div className="flex items-center gap-2">
                          <span className="form-label" style={{ marginBottom: 0, fontSize: '0.8125rem' }}>
                            {t('projectDetail.secrets.envVar')}
                          </span>
                          <input
                            value={binding.env_var_name}
                            onChange={e => updateField(sec.id, { env_var_name: e.target.value })}
                            placeholder="MY_SECRET"
                            className="form-input"
                            style={{
                              fontFamily: MONO, fontSize: '0.875rem',
                              padding: '4px 8px', width: '180px',
                            }}
                          />
                        </div>

                        {/* Build secret (all types) */}
                        <label className="flex items-center gap-2" style={{ cursor: 'pointer', fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                          <input
                            type="checkbox"
                            checked={binding.use_for_build}
                            onChange={() => updateField(sec.id, { use_for_build: !binding.use_for_build, build_secret_id: binding.build_secret_id || sec.name.toLowerCase().replace(/[^a-z0-9_]/g, '_') })}
                            style={{ accentColor: 'var(--accent)' }}
                          />
                          {t('projectDetail.secrets.useForBuild')}
                        </label>
                        {binding.use_for_build && (
                          <div className="flex items-center gap-2">
                            <span className="form-label" style={{ marginBottom: 0, fontSize: '0.8125rem' }}>
                              {t('projectDetail.secrets.buildSecretId')}
                            </span>
                            <input
                              value={binding.build_secret_id}
                              onChange={e => updateField(sec.id, { build_secret_id: e.target.value })}
                              placeholder="github_token"
                              className="form-input"
                              style={{
                                fontFamily: MONO, fontSize: '0.875rem',
                                padding: '4px 8px', width: '180px',
                              }}
                            />
                            <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                              {t('projectDetail.secrets.buildSecretHint')}
                            </span>
                          </div>
                        )}

                        {/* SSH key: use for git clone */}
                        {sec.type === 'ssh_key' && (
                          <label className="flex items-center gap-2" style={{ cursor: 'pointer', fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                            <input
                              type="checkbox"
                              checked={binding.use_for_git}
                              onChange={() => updateField(sec.id, { use_for_git: !binding.use_for_git })}
                              style={{ accentColor: 'var(--accent)' }}
                            />
                            {t('projectDetail.secrets.useForGitSsh')}
                          </label>
                        )}

                        {/* Password: use for HTTPS git auth */}
                        {sec.type === 'password' && (
                          <>
                            <label className="flex items-center gap-2" style={{ cursor: 'pointer', fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                              <input
                                type="checkbox"
                                checked={binding.use_for_git}
                                onChange={() => updateField(sec.id, { use_for_git: !binding.use_for_git, git_username: binding.git_username || 'x-access-token' })}
                                style={{ accentColor: 'var(--accent)' }}
                              />
                              {t('projectDetail.secrets.useForGitHttps')}
                            </label>
                            {binding.use_for_git && (
                              <div className="flex items-center gap-2">
                                <span className="form-label" style={{ marginBottom: 0, fontSize: '0.8125rem' }}>
                                  {t('projectDetail.secrets.username')}
                                </span>
                                <input
                                  value={binding.git_username}
                                  onChange={e => updateField(sec.id, { git_username: e.target.value })}
                                  placeholder="x-access-token"
                                  className="form-input"
                                  style={{
                                    fontFamily: MONO, fontSize: '0.875rem',
                                    padding: '4px 8px', width: '160px',
                                  }}
                                />
                                <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                                  {t('projectDetail.secrets.githubPat')} (<code style={{ fontFamily: MONO, color: 'var(--accent)' }}>x-access-token</code>)
                                </span>
                              </div>
                            )}
                          </>
                        )}

                      </div>
                    )}
                  </div>
                </div>
              </div>
            )
          })
        )}
      </div>
    </div>
  )
}

// ─── Push URL Badge (for hosted repos) ──────────────────────────────────────

function PushUrlBadge({ url, projectId }: { url: string; projectId: string }) {
  const [copied, setCopied] = useState(false)
  const [tokenCopied, setTokenCopied] = useState(false)
  const [generatedToken, setGeneratedToken] = useState<string | null>(null)
  const [generating, setGenerating] = useState(false)
  const [showToken, setShowToken] = useState(false)
  const [error, setError] = useState('')
  const { t } = useTranslation()

  const urlWithToken = generatedToken
    ? url.replace(/^(https?:\/\/)/, (_m, scheme) => `${scheme}x:${generatedToken}@`)
    : url

  const copy = () => {
    navigator.clipboard.writeText(url)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const copyWithToken = () => {
    navigator.clipboard.writeText(urlWithToken)
    setTokenCopied(true)
    setTimeout(() => setTokenCopied(false), 2000)
  }

  const generate = async () => {
    setGenerating(true)
    setError('')
    try {
      const result = await api.tokens.create(projectId, t('newProject.hostedCreated.tokenName'))
      setGeneratedToken(result.token)
      setShowToken(true)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setGenerating(false)
    }
  }

  return (
    <div className="flex flex-col gap-2 mt-2 ml-7" style={{ maxWidth: '640px' }}>
      <div className="flex items-center gap-2">
        <span className="page-subtitle" style={{ fontSize: '0.8125rem' }}>{t('projectDetail.pushUrl')}</span>
        <code style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--accent)', wordBreak: 'break-all' }}>{url}</code>
        <button onClick={copy} style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '2px' }}>
          {copied ? <Check size={12} style={{ color: 'var(--accent)' }} /> : <Copy size={12} />}
        </button>
        {!generatedToken && (
          <button
            type="button"
            onClick={generate}
            disabled={generating}
            title={t('projectDetail.pushUrlGenerateTokenTooltip')}
            style={{ background: 'none', border: '1px solid var(--border)', cursor: 'pointer', color: 'var(--fg-muted)', padding: '2px 6px', borderRadius: '4px', fontSize: '0.75rem', display: 'flex', alignItems: 'center', gap: '4px' }}
          >
            <Key size={11} />
            {generating ? t('newProject.hostedCreated.generating') : t('projectDetail.pushUrlGenerateToken')}
          </button>
        )}
      </div>
      {error && (
        <p style={{ fontSize: '0.8125rem', color: 'var(--danger)' }}>{error}</p>
      )}
      {generatedToken && (
        <div className="card" style={{
          background: 'rgba(37,99,235,0.08)', borderColor: 'rgba(37,99,235,0.3)',
          padding: '0.75rem 1rem',
        }}>
          <p style={{ fontSize: '0.8125rem', color: 'var(--accent)', marginBottom: '0.4rem', fontWeight: 600 }}>
            {t('newProject.hostedCreated.tokenReadyHeading')}
          </p>
          <div className="flex items-center gap-2" style={{ marginBottom: '0.5rem' }}>
            <code style={{
              fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)', flex: 1,
              wordBreak: 'break-all',
            }}>
              {showToken ? generatedToken : '•'.repeat(40)}
            </code>
            <button
              type="button"
              onClick={() => setShowToken(!showToken)}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px' }}
            >
              {showToken ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
          <div className="flex items-center gap-2">
            <code style={{
              flex: 1, fontFamily: MONO, fontSize: '0.8125rem', padding: '0.4rem 0.6rem',
              background: 'var(--bg-hover)', border: '1px solid var(--border)', borderRadius: '4px',
              color: 'var(--fg-primary)', wordBreak: 'break-all',
            }}>{urlWithToken}</code>
            <button
              type="button"
              onClick={copyWithToken}
              className="btn-secondary flex items-center gap-1"
              style={{
                fontSize: '0.8125rem', padding: '3px 8px',
                color: tokenCopied ? 'var(--success)' : 'var(--fg-muted)',
              }}
            >
              {tokenCopied ? <Check size={12} /> : <Copy size={12} />}
            </button>
          </div>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.4rem' }}>
            {t('newProject.hostedCreated.tokenWarning')}
          </p>
        </div>
      )}
    </div>
  )
}

// ─── Repository Browser Tab ─────────────────────────────────────────────────

function RepoTab({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const [branches, setBranches] = useState<RepoBranch[]>([])
  const [currentBranch, setCurrentBranch] = useState('')
  const [tree, setTree] = useState<RepoTreeEntry[]>([])
  const [commits, setCommits] = useState<RepoCommit[]>([])
  const [currentPath, setCurrentPath] = useState('')
  const [fileContent, setFileContent] = useState<string | null>(null)
  const [viewingFile, setViewingFile] = useState('')
  const [loading, setLoading] = useState(true)
  const [subTab, setSubTab] = useState<'files' | 'commits'>('files')

  useEffect(() => {
    api.projects.repoBranches(projectId).then(b => {
      setBranches(b)
      const def = b.find(br => br.is_default)
      setCurrentBranch(def?.name || b[0]?.name || 'main')
    }).catch(() => {})
    setLoading(false)
  }, [projectId])

  useEffect(() => {
    if (!currentBranch) return
    setLoading(true)
    Promise.all([
      api.projects.repoTree(projectId, currentBranch, currentPath).catch(() => []),
      api.projects.repoCommits(projectId, currentBranch, 20).catch(() => []),
    ]).then(([t, c]) => {
      setTree(t)
      setCommits(c)
      setLoading(false)
    })
  }, [projectId, currentBranch, currentPath])

  const navigateTo = (entry: RepoTreeEntry) => {
    if (entry.type === 'tree') {
      setCurrentPath(entry.path)
      setFileContent(null)
      setViewingFile('')
    } else {
      setViewingFile(entry.path)
      api.projects.repoBlob(projectId, currentBranch, entry.path).then(setFileContent).catch(() => setFileContent('(failed to load)'))
    }
  }

  const goUp = () => {
    const parts = currentPath.split('/').filter(Boolean)
    parts.pop()
    setCurrentPath(parts.join('/'))
    setFileContent(null)
    setViewingFile('')
  }

  const isEmpty = branches.length === 0 && !loading

  if (isEmpty) {
    return <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>{t('projectDetail.repo.empty')}</p>
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Branch selector + sub-tabs */}
      <div className="flex items-center gap-4">
        <select
          value={currentBranch}
          onChange={e => { setCurrentBranch(e.target.value); setCurrentPath(''); setFileContent(null); setViewingFile('') }}
          className="form-input"
          style={{
            fontSize: '0.875rem', padding: '0.35rem 0.5rem',
            cursor: 'pointer',
          }}
        >
          {branches.map(b => <option key={b.name} value={b.name}>{b.name}</option>)}
        </select>
        <div className="flex gap-1">
          {(['files', 'commits'] as const).map(st => (
            <button key={st} onClick={() => setSubTab(st)} className={subTab === st ? 'btn-primary' : 'btn-secondary'} style={{
              fontSize: '0.8125rem', padding: '0.3rem 0.6rem',
            }}>{t(`projectDetail.repo.${st}`)}</button>
          ))}
        </div>
      </div>

      {loading && <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>Loading...</p>}

      {/* File browser */}
      {!loading && subTab === 'files' && !viewingFile && (
        <div className="card" style={{ overflow: 'hidden' }}>
          {currentPath && (
            <button onClick={goUp} className="w-full text-left px-3 py-2 flex items-center gap-2" style={{
              fontSize: '0.875rem', color: 'var(--fg-muted)',
              background: 'none', border: 'none', borderBottom: '1px solid var(--border)', cursor: 'pointer',
            }}
              onMouseEnter={e => { e.currentTarget.style.background = 'var(--bg-hover)' }}
              onMouseLeave={e => { e.currentTarget.style.background = 'none' }}
            >
              ..
            </button>
          )}
          {tree.map(entry => (
            <button key={entry.path} onClick={() => navigateTo(entry)} className="w-full text-left px-3 py-2 flex items-center gap-2" style={{
              fontSize: '0.875rem', color: 'var(--fg-primary)',
              background: 'none', border: 'none', borderBottom: '1px solid var(--border)', cursor: 'pointer',
            }}
              onMouseEnter={e => { e.currentTarget.style.background = 'var(--bg-hover)' }}
              onMouseLeave={e => { e.currentTarget.style.background = 'none' }}
            >
              {entry.type === 'tree' ? <FolderOpen size={14} style={{ color: 'var(--accent)' }} /> : <File size={14} style={{ color: 'var(--fg-muted)' }} />}
              <span className="flex-1" style={{ fontFamily: MONO }}>{entry.name}</span>
              {entry.type === 'blob' && entry.size > 0 && (
                <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{formatBytes(entry.size)}</span>
              )}
            </button>
          ))}
          {tree.length === 0 && (
            <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', padding: '1rem', textAlign: 'center' }}>
              {t('projectDetail.repo.empty')}
            </p>
          )}
        </div>
      )}

      {/* File content viewer */}
      {!loading && subTab === 'files' && viewingFile && fileContent !== null && (
        <div>
          <div className="flex items-center gap-2 mb-2">
            <button onClick={() => { setViewingFile(''); setFileContent(null) }} style={{
              fontSize: '0.8125rem', color: 'var(--accent)',
              background: 'none', border: 'none', cursor: 'pointer',
            }}>← {t('projectDetail.repo.files')}</button>
            <span style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{viewingFile}</span>
          </div>
          <pre style={{
            fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-primary)',
            background: 'var(--bg-hover)', border: '1px solid var(--border)', borderRadius: '6px',
            padding: '1rem', overflow: 'auto', maxHeight: '600px', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
          }}>{fileContent}</pre>
        </div>
      )}

      {/* Commits list */}
      {!loading && subTab === 'commits' && (
        <div className="card" style={{ overflow: 'hidden' }}>
          {commits.length === 0 && (
            <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', padding: '1rem', textAlign: 'center' }}>
              {t('projectDetail.repo.noCommits')}
            </p>
          )}
          {commits.map(c => (
            <div key={c.sha} style={{
              padding: '0.6rem 0.75rem', borderBottom: '1px solid var(--border)',
              fontSize: '0.875rem',
            }}>
              <div className="flex items-center gap-2">
                <code style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--accent)' }}>{c.sha.substring(0, 8)}</code>
                <span style={{ color: 'var(--fg-primary)', flex: 1 }}>{c.message}</span>
              </div>
              <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.2rem' }}>
                {c.author} · {timeAgo(c.date)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Tokens Tab ──────────────────────────────────────────────────────────────

function TokensTab({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [createdToken, setCreatedToken] = useState<string | null>(null)
  const [showToken, setShowToken] = useState(false)
  const [copied, setCopied] = useState(false)

  const load = () => {
    api.tokens.list(projectId).then(setTokens).catch(() => {}).finally(() => setLoading(false))
  }
  useEffect(load, [projectId])

  const handleCreate = async () => {
    setCreating(true)
    try {
      const result = await api.tokens.create(projectId, newName || 'Git Token')
      setCreatedToken(result.token)
      setNewName('')
      load()
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async (tokenId: string) => {
    if (!confirm(t('projectDetail.tokens.deleteConfirm'))) return
    await api.tokens.delete(projectId, tokenId)
    load()
  }

  const copyToken = (val: string) => {
    navigator.clipboard.writeText(val)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div>
      <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', marginBottom: '1rem', lineHeight: 1.6 }}>
        {t('projectDetail.tokens.hint')}
      </p>

      {/* Create token form */}
      <div className="flex items-center gap-2 mb-4">
        <input
          type="text"
          value={newName}
          onChange={e => setNewName(e.target.value)}
          placeholder={t('projectDetail.tokens.namePlaceholder')}
          className="form-input"
          style={{
            fontSize: '0.875rem',
            flex: 1, maxWidth: '300px',
          }}
          onKeyDown={e => e.key === 'Enter' && handleCreate()}
        />
        <button
          onClick={handleCreate}
          disabled={creating}
          className="btn-primary flex items-center gap-1.5"
          style={creating ? { cursor: 'not-allowed', opacity: 0.6 } : {}}
        >
          <Plus size={13} />
          {creating ? t('projectDetail.tokens.creating') : t('projectDetail.tokens.create')}
        </button>
      </div>

      {/* Newly created token */}
      {createdToken && (
        <div className="card" style={{
          background: 'rgba(37,99,235,0.08)', borderColor: 'rgba(37,99,235,0.3)',
          padding: '0.75rem 1rem', marginBottom: '1rem',
        }}>
          <p style={{ fontSize: '0.8125rem', color: 'var(--accent)', marginBottom: '0.4rem', fontWeight: 600 }}>
            {t('projectDetail.tokens.created')}
          </p>
          <div className="flex items-center gap-2">
            <code style={{
              fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)', flex: 1,
              wordBreak: 'break-all',
            }}>
              {showToken ? createdToken : '•'.repeat(40)}
            </code>
            <button
              onClick={() => setShowToken(!showToken)}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px' }}
            >
              {showToken ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
            <button
              onClick={() => copyToken(createdToken)}
              className="btn-secondary flex items-center gap-1"
              style={{
                fontSize: '0.8125rem', padding: '3px 8px',
                color: copied ? 'var(--success)' : 'var(--fg-muted)',
              }}
            >
              {copied ? <Check size={12} /> : <Copy size={12} />}
              {copied ? t('projects.copied') : t('projects.copy')}
            </button>
          </div>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.4rem' }}>
            {t('projectDetail.tokens.copyWarning')}
          </p>
        </div>
      )}

      {/* Token list */}
      {loading ? (
        <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>{t('common.loading')}</p>
      ) : tokens.length === 0 && !createdToken ? (
        <div className="card" style={{
          padding: '2rem',
          textAlign: 'center',
        }}>
          <Key size={28} style={{ color: 'var(--fg-muted)', margin: '0 auto 0.5rem' }} />
          <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
            {t('projectDetail.tokens.empty')}
          </p>
        </div>
      ) : (
        <div className="table-container">
          <table className="w-full border-collapse">
            <thead>
              <tr>
                {[t('projectDetail.tokens.columns.name'), t('projectDetail.tokens.columns.lastUsed'), t('projectDetail.tokens.columns.created'), ''].map(h => (
                  <th key={h} style={{
                    fontSize: '0.8125rem', color: 'var(--fg-muted)', letterSpacing: '0.04em',
                    padding: '0.6rem 1rem', textAlign: 'left', fontWeight: 500,
                  }}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {tokens.map(tok => (
                <tr key={tok.id}
                  onMouseEnter={e => { (e.currentTarget as HTMLTableRowElement).style.background = 'var(--bg-hover)' }}
                  onMouseLeave={e => { (e.currentTarget as HTMLTableRowElement).style.background = 'var(--bg-card)' }}
                >
                  <td style={{ padding: '0.7rem 1rem' }}>
                    <span style={{ fontSize: '0.875rem', color: 'var(--fg-primary)', fontWeight: 500 }}>
                      {tok.name}
                    </span>
                  </td>
                  <td style={{ padding: '0.7rem 1rem' }}>
                    <span style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                      {tok.last_used_at ? timeAgo(tok.last_used_at) : '—'}
                    </span>
                  </td>
                  <td style={{ padding: '0.7rem 1rem' }}>
                    <span style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                      {new Date(tok.created_at).toLocaleDateString()}
                    </span>
                  </td>
                  <td style={{ padding: '0.7rem 1rem', textAlign: 'right' }}>
                    <button
                      onClick={() => handleDelete(tok.id)}
                      style={{
                        background: 'none', border: '1px solid var(--border)', borderRadius: '6px',
                        padding: '3px 8px', cursor: 'pointer', color: 'var(--fg-muted)',
                        fontSize: '0.8125rem',
                      }}
                      onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--danger)'; (e.currentTarget as HTMLButtonElement).style.borderColor = 'var(--danger)' }}
                      onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)'; (e.currentTarget as HTMLButtonElement).style.borderColor = 'var(--border)' }}
                    >
                      <Trash2 size={12} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
