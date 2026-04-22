import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlusCircle, GitBranch, Globe, Circle, Terminal, Copy, Check, ExternalLink, Radio, User } from 'lucide-react'
import { api } from '../lib/api'
import type { Project } from '../lib/types'
import { timeAgo, statusColor } from '../lib/utils'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

function statusBadgeClass(status: string): string {
  switch (status) {
    case 'running': return 'badge badge-success'
    case 'building': case 'deploying': return 'badge badge-warning'
    case 'failed': return 'badge badge-danger'
    case 'stopped': return 'badge badge-neutral'
    default: return 'badge badge-neutral'
  }
}

export default function Projects() {
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const { t } = useTranslation()

  useEffect(() => {
    api.projects.list().then(setProjects).finally(() => setLoading(false))
  }, [])

  return (
    <div className="page-enter">
      <div className="page-header flex items-end justify-between">
        <div>
          <p className="page-subtitle">
            {t('projects.sectionLabel')}
          </p>
          <h1 className="page-title">
            {t('projects.heading')}
          </h1>
        </div>
        <Link
          to="/projects/new"
          className="btn-primary flex items-center gap-2"
          style={{ textDecoration: 'none' }}
        >
          <PlusCircle size={14} />
          {t('projects.newProject')}
        </Link>
      </div>

      {loading ? (
        <div style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>{t('projects.loading')}</div>
      ) : projects.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="card" style={{ overflow: 'hidden' }}>
          {projects.map((p, i) => (
            <ProjectRow key={p.id} project={p} index={i} total={projects.length} />
          ))}
        </div>
      )}

      <CLIAccessCard />
    </div>
  )
}

function ProjectRow({ project, index, total }: { project: Project; index: number; total: number }) {
  const [latestDeploy, setLatestDeploy] = useState<import('../lib/types').Deployment | null>(null)
  const { t } = useTranslation()
  const isTunnel = project.project_type === 'domain_only'

  useEffect(() => {
    if (isTunnel) return
    api.projects.deployments(project.id)
      .then(ds => setLatestDeploy(ds?.[0] ?? null))
      .catch(() => {})
  }, [project.id, isTunnel])

  const status = isTunnel ? 'tunnel' : (latestDeploy?.status ?? 'pending')
  const color = isTunnel ? 'var(--accent)' : statusColor(latestDeploy?.status ?? 'pending')

  const STATUS_LABELS: Record<string, string> = {
    running: t('projects.status.running'),
    building: t('projects.status.building'),
    deploying: t('projects.status.deploying'),
    failed: t('projects.status.failed'),
    stopped: t('projects.status.stopped'),
    pending: t('projects.status.pending'),
    tunnel: t('projects.status.tunnel'),
  }

  return (
    <Link
      to={`/projects/${project.id}`}
      className="flex items-center gap-5 px-5 py-4 transition-all duration-150"
      style={{
        background: 'var(--bg-card)',
        textDecoration: 'none',
        borderBottom: index < total - 1 ? '1px solid var(--border)' : 'none',
      }}
      onMouseEnter={e => { (e.currentTarget as HTMLAnchorElement).style.background = 'var(--bg-hover)' }}
      onMouseLeave={e => { (e.currentTarget as HTMLAnchorElement).style.background = 'var(--bg-card)' }}
    >
      {/* Status dot */}
      <div className="flex-shrink-0">
        <Circle
          size={8}
          fill={color}
          stroke="none"
          className={status === 'running' ? 'status-running' : ''}
        />
      </div>

      {/* Name */}
      <div className="flex-1 min-w-0">
        <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', lineHeight: 1.3 }}>
          {project.name}
        </div>
        {isTunnel ? (
          <div className="flex items-center gap-2 mt-1">
            <span style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
              <Radio size={10} className="inline mr-1" />
              tunnel
            </span>
          </div>
        ) : (
          <div className="flex items-center gap-2 mt-1">
            <span style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
              <GitBranch size={10} className="inline mr-1" />
              {project.git_branch}
            </span>
            <span style={{ color: 'var(--border)' }}>·</span>
            <span style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '200px' }}>
              {project.git_url.replace(/^https?:\/\//, '')}
            </span>
          </div>
        )}
      </div>

      {/* Owner */}
      {project.owner_name && (
        <div
          className="hidden md:flex items-center gap-1 flex-shrink-0"
          style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', maxWidth: '160px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
          title={project.owner_email || project.owner_name}
        >
          <User size={11} />
          {project.owner_name}
        </div>
      )}

      {/* Domain */}
      <div className="hidden md:flex items-center gap-1 flex-shrink-0" style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
        <Globe size={11} />
        {project.domain_prefix}.domain
      </div>

      {/* Status badge */}
      <div className={`flex-shrink-0 ${statusBadgeClass(status)}`}>
        {STATUS_LABELS[status]}
      </div>

      {/* Time */}
      <div className="hidden lg:block flex-shrink-0" style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', minWidth: '60px', textAlign: 'right' }}>
        {latestDeploy ? timeAgo(latestDeploy.updated_at) : '—'}
      </div>
    </Link>
  )
}

function EmptyState() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center py-20 gap-3">
      <div style={{ fontSize: '2.5rem', fontWeight: 700, color: 'var(--border)' }}>
        {t('projects.noProjects')}
      </div>
      <p style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
        {t('projects.noProjectsHint')}
      </p>
    </div>
  )
}

// ─── CLI Access Card ──────────────────────────────────────────────────────────

function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const { t } = useTranslation()
  const copy = () => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <button
      onClick={copy}
      className="btn-secondary flex items-center gap-1.5"
      style={{
        fontFamily: MONO,
        fontSize: '0.8125rem',
        background: copied ? 'rgba(88,166,255,0.1)' : undefined,
        color: copied ? 'var(--accent)' : undefined,
        borderColor: copied ? 'rgba(88,166,255,0.4)' : undefined,
      }}
      title={`Copy ${label ?? text}`}
    >
      {copied ? <Check size={11} /> : <Copy size={11} />}
      {copied ? t('projects.copied') : (label ?? t('projects.copy'))}
    </button>
  )
}

function CLIAccessCard() {
  const [open, setOpen] = useState(false)
  const { t } = useTranslation()
  const installCmd = `curl -fsSL ${window.location.origin}/api/install.sh | sh`
  const skillURL = `${window.location.origin}/api/skill`

  return (
    <div className="card mt-8" style={{ overflow: 'hidden' }}>
      {/* Header toggle */}
      <button
        className="w-full flex items-center gap-3 px-5 py-4 text-left transition-all duration-150"
        style={{ background: 'none', border: 'none', cursor: 'pointer' }}
        onClick={() => setOpen(o => !o)}
        onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--bg-hover)' }}
        onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'none' }}
      >
        <Terminal size={15} style={{ color: 'var(--accent)' }} />
        <div className="flex-1">
          <p className="page-subtitle" style={{ marginBottom: '2px' }}>
            {t('projects.developerAccess')}
          </p>
          <p style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', lineHeight: 1.2 }}>
            {t('projects.quickInstall')}
          </p>
        </div>
        <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
          {open ? '▲' : '▼'}
        </span>
      </button>

      {open && (
        <div style={{ borderTop: '1px solid var(--border)', padding: '1.5rem' }}>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', letterSpacing: '0.04em', marginBottom: '0.4rem', fontWeight: 500 }}>
            {t('projects.installCommand')}
          </p>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.6 }}>
            {t('projects.installCmdDesc')}
          </p>
          <div className="flex items-center gap-2">
            <div
              className="flex-1 px-3 py-2 rounded-md"
              style={{
                fontFamily: MONO,
                fontSize: '0.8125rem',
                color: 'var(--accent)',
                background: 'var(--bg-hover)',
                border: '1px solid var(--border)',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {installCmd}
            </div>
            <CopyButton text={installCmd} label={t('projects.copy')} />
            <a
              href={skillURL}
              target="_blank"
              rel="noopener noreferrer"
              className="btn-secondary flex items-center gap-1.5"
              style={{
                fontFamily: MONO,
                fontSize: '0.8125rem',
                textDecoration: 'none',
              }}
            >
              <ExternalLink size={11} />
              {t('projects.preview')}
            </a>
          </div>
        </div>
      )}
    </div>
  )
}
