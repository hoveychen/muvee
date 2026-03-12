import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlusCircle, GitBranch, Globe, Circle } from 'lucide-react'
import { api } from '../lib/api'
import type { Project } from '../lib/types'
import { timeAgo, statusColor } from '../lib/utils'

const STATUS_LABELS: Record<string, string> = {
  running: 'RUNNING',
  building: 'BUILDING',
  deploying: 'DEPLOYING',
  failed: 'FAILED',
  stopped: 'STOPPED',
  pending: 'IDLE',
}

export default function Projects() {
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.projects.list().then(setProjects).finally(() => setLoading(false))
  }, [])

  return (
    <div className="page-enter">
      <div className="flex items-end justify-between mb-10">
        <div>
          <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.15em' }}>
            PROJECTS
          </p>
          <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>
            All Projects
          </h1>
        </div>
        <Link
          to="/projects/new"
          className="flex items-center gap-2 px-4 py-2 rounded-sm text-sm transition-all duration-150"
          style={{
            background: 'var(--accent)',
            color: '#0f0f0f',
            fontFamily: 'DM Mono',
            fontWeight: 500,
            textDecoration: 'none',
          }}
        >
          <PlusCircle size={14} />
          New Project
        </Link>
      </div>

      {loading ? (
        <div style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>Loading...</div>
      ) : projects.length === 0 ? (
        <EmptyState />
      ) : (
        <div className="grid gap-px" style={{ background: 'var(--border)' }}>
          {projects.map((p, i) => (
            <ProjectRow key={p.id} project={p} index={i} />
          ))}
        </div>
      )}
    </div>
  )
}

function ProjectRow({ project, index }: { project: Project; index: number }) {
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
      className="flex items-center gap-6 px-6 py-5 transition-all duration-150"
      style={{
        background: 'var(--bg-card)',
        textDecoration: 'none',
        borderLeft: `3px solid ${index % 2 === 0 ? color : 'var(--border)'}`,
        animationDelay: `${index * 30}ms`,
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
        <div style={{ fontFamily: 'Bebas Neue', fontSize: '1.4rem', color: 'var(--fg-primary)', lineHeight: 1 }}>
          {project.name}
        </div>
        <div className="flex items-center gap-3 mt-1">
          <span style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)' }}>
            <GitBranch size={10} className="inline mr-1" />
            {project.git_branch}
          </span>
          <span style={{ color: 'var(--border)' }}>·</span>
          <span style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '200px' }}>
            {project.git_url.replace(/^https?:\/\//, '')}
          </span>
        </div>
      </div>

      {/* Domain */}
      <div className="hidden md:flex items-center gap-1 flex-shrink-0" style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
        <Globe size={11} />
        {project.domain_prefix}.domain
      </div>

      {/* Status badge */}
      <div
        className="flex-shrink-0 px-2 py-0.5 rounded-sm"
        style={{
          fontFamily: 'DM Mono',
          fontSize: '0.65rem',
          color: color,
          border: `1px solid ${color}22`,
          background: `${color}11`,
        }}
      >
        {STATUS_LABELS[status]}
      </div>

      {/* Time */}
      <div className="hidden lg:block flex-shrink-0" style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', minWidth: '60px', textAlign: 'right' }}>
        {latestDeploy ? timeAgo(latestDeploy.updated_at) : '—'}
      </div>
    </Link>
  )
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center py-24 gap-4">
      <div style={{ fontFamily: 'Bebas Neue', fontSize: '4rem', color: 'var(--border)' }}>
        NO PROJECTS
      </div>
      <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
        Create your first project to get started
      </p>
    </div>
  )
}
