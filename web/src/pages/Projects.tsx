import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { PlusCircle, GitBranch, Globe, Circle, Terminal, Copy, Check, Trash2, Plus, ExternalLink } from 'lucide-react'
import { api } from '../lib/api'
import type { Project, ApiToken, CreatedApiToken } from '../lib/types'
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

      <CLIAccessCard />
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
      className="flex items-center gap-1.5 px-2 py-1 rounded-sm transition-all duration-150"
      style={{
        fontFamily: 'DM Mono',
        fontSize: '0.7rem',
        background: copied ? 'var(--accent)11' : 'var(--bg-hover)',
        color: copied ? 'var(--accent)' : 'var(--fg-muted)',
        border: `1px solid ${copied ? 'var(--accent)44' : 'var(--border)'}`,
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
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [newToken, setNewToken] = useState<CreatedApiToken | null>(null)
  const [creating, setCreating] = useState(false)
  const [tokenName, setTokenName] = useState('CLI Token')
  const [open, setOpen] = useState(false)

  const skillURL = `${window.location.origin}/api/skill`
  const serverURL = window.location.origin

  useEffect(() => {
    if (open) {
      api.tokens.list().then(setTokens).catch(() => {})
    }
  }, [open])

  const handleCreate = async () => {
    setCreating(true)
    try {
      const t = await api.tokens.create(tokenName || 'CLI Token')
      setNewToken(t)
      setTokens(prev => [{ id: t.id, name: t.name, last_used_at: null, created_at: new Date().toISOString() }, ...prev])
    } catch (e) {
      alert('Failed to create token: ' + (e as Error).message)
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm('Revoke this token? This cannot be undone.')) return
    await api.tokens.delete(id).catch(() => {})
    setTokens(prev => prev.filter(t => t.id !== id))
    if (newToken?.id === id) setNewToken(null)
  }

  return (
    <div className="mt-10" style={{ border: '1px solid var(--border)', background: 'var(--bg-card)' }}>
      {/* Header toggle */}
      <button
        className="w-full flex items-center gap-3 px-6 py-4 text-left transition-all duration-150"
        style={{ background: 'none', border: 'none', cursor: 'pointer' }}
        onClick={() => setOpen(o => !o)}
        onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--bg-hover)' }}
        onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'none' }}
      >
        <Terminal size={16} style={{ color: 'var(--accent)' }} />
        <div className="flex-1">
          <p style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', letterSpacing: '0.1em' }}>
            DEVELOPER ACCESS
          </p>
          <p style={{ fontFamily: 'Bebas Neue', fontSize: '1.2rem', color: 'var(--fg-primary)', lineHeight: 1 }}>
            CLI &amp; API Tokens
          </p>
        </div>
        <span style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
          {open ? '▲' : '▼'}
        </span>
      </button>

      {open && (
        <div style={{ borderTop: '1px solid var(--border)', padding: '1.5rem' }}>
          {/* Skill URL */}
          <div className="mb-6">
            <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em', marginBottom: '0.5rem' }}>
              CLAUDE SKILL URL
            </p>
            <p style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', marginBottom: '0.75rem' }}>
              Share this URL with Claude to teach it how to use the muveectl CLI on your behalf.
            </p>
            <div className="flex items-center gap-2">
              <div
                className="flex-1 px-3 py-2 rounded-sm"
                style={{
                  fontFamily: 'DM Mono',
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
                className="flex items-center gap-1.5 px-2 py-1 rounded-sm transition-all duration-150"
                style={{
                  fontFamily: 'DM Mono',
                  fontSize: '0.7rem',
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

          {/* Quick start */}
          <div className="mb-6">
            <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em', marginBottom: '0.5rem' }}>
              QUICK START
            </p>
            <div className="flex flex-col gap-2">
              {[
                { label: 'Login', cmd: `muveectl login --server ${serverURL}` },
                { label: 'List projects', cmd: 'muveectl projects list' },
                { label: 'Deploy', cmd: 'muveectl projects deploy <PROJECT_ID>' },
              ].map(({ label, cmd }) => (
                <div key={label} className="flex items-center gap-2">
                  <div
                    className="flex-1 px-3 py-1.5 rounded-sm"
                    style={{
                      fontFamily: 'DM Mono',
                      fontSize: '0.72rem',
                      color: 'var(--fg-primary)',
                      background: '#0a0a0a',
                      border: '1px solid var(--border)',
                    }}
                  >
                    <span style={{ color: 'var(--accent)', marginRight: '0.5rem' }}>$</span>
                    {cmd}
                  </div>
                  <CopyButton text={cmd} label={label} />
                </div>
              ))}
            </div>
          </div>

          {/* API Tokens */}
          <div>
            <div className="flex items-center justify-between mb-3">
              <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.12em' }}>
                API TOKENS
              </p>
              <div className="flex items-center gap-2">
                <input
                  value={tokenName}
                  onChange={e => setTokenName(e.target.value)}
                  placeholder="Token name"
                  style={{
                    fontFamily: 'DM Mono',
                    fontSize: '0.72rem',
                    padding: '0.25rem 0.6rem',
                    background: 'var(--bg-hover)',
                    border: '1px solid var(--border)',
                    color: 'var(--fg-primary)',
                    borderRadius: '2px',
                    outline: 'none',
                    width: '140px',
                  }}
                />
                <button
                  onClick={handleCreate}
                  disabled={creating}
                  className="flex items-center gap-1.5 px-2 py-1 rounded-sm transition-all duration-150"
                  style={{
                    fontFamily: 'DM Mono',
                    fontSize: '0.7rem',
                    background: 'var(--accent)',
                    color: '#0f0f0f',
                    border: 'none',
                    cursor: creating ? 'not-allowed' : 'pointer',
                    opacity: creating ? 0.6 : 1,
                  }}
                >
                  <Plus size={11} />
                  {creating ? 'Creating…' : 'New Token'}
                </button>
              </div>
            </div>

            {/* Newly created token banner */}
            {newToken && (
              <div
                className="mb-3 px-4 py-3 rounded-sm"
                style={{ background: 'var(--accent)11', border: '1px solid var(--accent)44' }}
              >
                <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--accent)', marginBottom: '0.4rem', letterSpacing: '0.1em' }}>
                  NEW TOKEN — COPY NOW, IT WON'T BE SHOWN AGAIN
                </p>
                <div className="flex items-center gap-2">
                  <div
                    className="flex-1 px-3 py-1.5 rounded-sm"
                    style={{
                      fontFamily: 'DM Mono',
                      fontSize: '0.72rem',
                      color: 'var(--accent)',
                      background: '#0a0a0a',
                      border: '1px solid var(--accent)44',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {newToken.token}
                  </div>
                  <CopyButton text={newToken.token} label="Copy Token" />
                </div>
              </div>
            )}

            {/* Token list */}
            {tokens.length === 0 ? (
              <p style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                No tokens yet. Create one to use the CLI.
              </p>
            ) : (
              <div className="grid gap-px" style={{ background: 'var(--border)' }}>
                {tokens.map(t => (
                  <div
                    key={t.id}
                    className="flex items-center gap-4 px-4 py-2.5"
                    style={{ background: 'var(--bg-card)' }}
                  >
                    <div className="flex-1 min-w-0">
                      <p style={{ fontFamily: 'DM Mono', fontSize: '0.8rem', color: 'var(--fg-primary)' }}>{t.name}</p>
                      <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                        Created {timeAgo(t.created_at)}
                        {t.last_used_at ? ` · Last used ${timeAgo(t.last_used_at)}` : ' · Never used'}
                      </p>
                    </div>
                    <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
                      {t.id.slice(0, 8)}…
                    </p>
                    <button
                      onClick={() => handleDelete(t.id)}
                      className="p-1 rounded-sm transition-all duration-150"
                      style={{ background: 'none', border: 'none', color: 'var(--fg-muted)', cursor: 'pointer' }}
                      onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = '#ef4444' }}
                      onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
                      title="Revoke token"
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
