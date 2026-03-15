import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlusCircle, GitBranch, Globe, Circle, Terminal, Copy, Check, ExternalLink } from 'lucide-react'
import { api } from '../lib/api'
import type { Project } from '../lib/types'
import { timeAgo, statusColor } from '../lib/utils'

const MONO = 'var(--font-mono)'

const STATUS_LABELS: Record<string, string> = {
  running: 'running',
  building: 'building',
  deploying: 'deploying',
  failed: 'failed',
  stopped: 'stopped',
  pending: 'idle',
}

export default function Projects() {
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.projects.list().then(setProjects).finally(() => setLoading(false))
  }, [])

  return (
    <div className="page-enter">
      <div className="flex items-end justify-between mb-8">
        <div>
          <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.72rem', letterSpacing: '0.05em' }}>
            PROJECTS
          </p>
          <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>
            All Projects
          </h1>
        </div>
        <Link
          to="/projects/new"
          className="flex items-center gap-2 px-3 py-1.5 rounded-md text-sm transition-all duration-150"
          style={{
            background: 'var(--accent)',
            color: '#ffffff',
            fontWeight: 500,
            textDecoration: 'none',
          }}
        >
          <PlusCircle size={14} />
          New Project
        </Link>
      </div>

      {loading ? (
        <div style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem' }}>Loading...</div>
      ) : projects.length === 0 ? (
        <EmptyState />
      ) : (
        <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
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

  useEffect(() => {
    api.projects.deployments(project.id)
      .then(ds => setLatestDeploy(ds?.[0] ?? null))
      .catch(() => {})
  }, [project.id])

  const status = latestDeploy?.status ?? 'pending'
  const color = statusColor(status)

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
        <div style={{ fontSize: '0.95rem', fontWeight: 600, color: 'var(--fg-primary)', lineHeight: 1.3 }}>
          {project.name}
        </div>
        <div className="flex items-center gap-2 mt-1">
          <span style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)' }}>
            <GitBranch size={10} className="inline mr-1" />
            {project.git_branch}
          </span>
          <span style={{ color: 'var(--border)' }}>·</span>
          <span style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '200px' }}>
            {project.git_url.replace(/^https?:\/\//, '')}
          </span>
        </div>
      </div>

      {/* Domain */}
      <div className="hidden md:flex items-center gap-1 flex-shrink-0" style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
        <Globe size={11} />
        {project.domain_prefix}.domain
      </div>

      {/* Status badge */}
      <div
        className="flex-shrink-0 px-2 py-0.5 rounded-full"
        style={{
          fontFamily: MONO,
          fontSize: '0.68rem',
          color: color,
          border: `1px solid ${color}44`,
          background: `${color}18`,
        }}
      >
        {STATUS_LABELS[status]}
      </div>

      {/* Time */}
      <div className="hidden lg:block flex-shrink-0" style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', minWidth: '60px', textAlign: 'right' }}>
        {latestDeploy ? timeAgo(latestDeploy.updated_at) : '—'}
      </div>
    </Link>
  )
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center py-20 gap-3">
      <div style={{ fontSize: '2.5rem', fontWeight: 700, color: 'var(--border)' }}>
        No projects
      </div>
      <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
        Create your first project to get started
      </p>
    </div>
  )
}

// ─── CLI Access Card ──────────────────────────────────────────────────────────

function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }
  return (
    <button
      onClick={copy}
      className="flex items-center gap-1.5 px-2 py-1 rounded-md transition-all duration-150"
      style={{
        fontFamily: MONO,
        fontSize: '0.72rem',
        background: copied ? 'rgba(88,166,255,0.1)' : 'var(--bg-hover)',
        color: copied ? 'var(--accent)' : 'var(--fg-muted)',
        border: `1px solid ${copied ? 'rgba(88,166,255,0.4)' : 'var(--border)'}`,
        cursor: 'pointer',
      }}
      title={`Copy ${label ?? text}`}
    >
      {copied ? <Check size={11} /> : <Copy size={11} />}
      {copied ? 'Copied!' : (label ?? 'Copy')}
    </button>
  )
}

function CLIAccessCard() {
  const [open, setOpen] = useState(false)
  const skillURL = `${window.location.origin}/api/skill`

  return (
    <div className="mt-8" style={{ border: '1px solid var(--border)', borderRadius: '6px', background: 'var(--bg-card)', overflow: 'hidden' }}>
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
          <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', letterSpacing: '0.05em' }}>
            DEVELOPER ACCESS
          </p>
          <p style={{ fontSize: '0.9rem', fontWeight: 600, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '2px' }}>
            Skill URL
          </p>
        </div>
        <span style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
          {open ? '▲' : '▼'}
        </span>
      </button>

      {open && (
        <div style={{ borderTop: '1px solid var(--border)', padding: '1.5rem' }}>
          <p style={{ fontFamily: MONO, fontSize: '0.68rem', color: 'var(--fg-muted)', letterSpacing: '0.08em', marginBottom: '0.4rem' }}>
            CLAUDE SKILL URL
          </p>
          <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginBottom: '0.75rem', lineHeight: 1.6 }}>
            Share this URL with Claude to teach it how to use the muveectl CLI on your behalf.
          </p>
          <div className="flex items-center gap-2">
            <div
              className="flex-1 px-3 py-2 rounded-md"
              style={{
                fontFamily: MONO,
                fontSize: '0.75rem',
                color: 'var(--accent)',
                background: 'var(--bg-hover)',
                border: '1px solid var(--border)',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {skillURL}
            </div>
            <CopyButton text={skillURL} label="Copy URL" />
            <a
              href={skillURL}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1.5 px-2 py-1 rounded-md transition-all duration-150"
              style={{
                fontFamily: MONO,
                fontSize: '0.72rem',
                background: 'var(--bg-hover)',
                color: 'var(--fg-muted)',
                border: '1px solid var(--border)',
                textDecoration: 'none',
              }}
            >
              <ExternalLink size={11} />
              Preview
            </a>
          </div>
        </div>
      )}
    </div>
  )
}
