import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Rocket, Settings, Database, ChevronDown, ChevronUp, Trash2, ArrowLeft } from 'lucide-react'
import { api } from '../lib/api'
import type { Dataset, Deployment, Project, ProjectDataset } from '../lib/types'
import { statusColor, timeAgo, formatBytes, isValidDomainPrefix } from '../lib/utils'

type Tab = 'deploy' | 'config' | 'datasets'

export default function ProjectDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [project, setProject] = useState<Project | null>(null)
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [datasets, setDatasets] = useState<Dataset[]>([])
  const [projectDatasets, setProjectDatasets] = useState<ProjectDataset[]>([])
  const [availableDatasets, setAvailableDatasets] = useState<Dataset[]>([])
  const [tab, setTab] = useState<Tab>('deploy')
  const [deploying, setDeploying] = useState(false)
  const [editForm, setEditForm] = useState<Partial<Project>>({})
  const [saving, setSaving] = useState(false)
  const pollRef = useRef<ReturnType<typeof setInterval>>(null)

  useEffect(() => {
    if (!id) return
    api.projects.get(id).then(p => { setProject(p); setEditForm(p) })
    api.projects.deployments(id).then(setDeployments)
    api.projects.datasets(id).then(setProjectDatasets)
    api.datasets.list().then(setAvailableDatasets)
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
    try {
      const updated = await api.projects.update(id, editForm)
      setProject(updated)
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async () => {
    if (!id || !confirm('Delete this project?')) return
    await api.projects.delete(id)
    navigate('/projects')
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

  if (!project) return <div style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', padding: '2rem' }}>Loading...</div>

  const latestDeploy = deployments[0]
  const color = statusColor(latestDeploy?.status ?? 'pending')

  return (
    <div className="page-enter">
      {/* Header */}
      <div className="mb-8">
        <button
          onClick={() => navigate('/projects')}
          className="flex items-center gap-1 mb-4 text-xs transition-colors"
          style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
        >
          <ArrowLeft size={12} /> Back
        </button>
        <div className="flex items-end justify-between">
          <div>
            <div
              className="w-2 h-2 rounded-full inline-block mr-3 mb-1"
              style={{ background: color }}
            />
            <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3.5rem', color: 'var(--fg-primary)', lineHeight: 0.9, display: 'inline' }}>
              {project.name}
            </h1>
          </div>
          <button
            onClick={handleDeploy}
            disabled={deploying}
            className="flex items-center gap-2 px-5 py-2.5 rounded-sm text-sm transition-all duration-150"
            style={{
              background: deploying ? 'var(--bg-hover)' : 'var(--accent)',
              color: deploying ? 'var(--fg-muted)' : '#0f0f0f',
              fontFamily: 'DM Mono',
              fontWeight: 500,
              border: 'none',
              cursor: deploying ? 'not-allowed' : 'pointer',
            }}
          >
            <Rocket size={14} />
            {deploying ? 'Triggering...' : 'Deploy'}
          </button>
        </div>
        <div style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.5rem' }}>
          {project.domain_prefix}.domain.com
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-0 mb-0" style={{ borderBottom: '1px solid var(--border)' }}>
        {([['deploy', Rocket, 'Deployments'], ['config', Settings, 'Config'], ['datasets', Database, 'Datasets']] as const).map(([key, Icon, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className="flex items-center gap-2 px-5 py-3 text-sm transition-all duration-150"
            style={{
              fontFamily: 'DM Mono',
              color: tab === key ? 'var(--accent)' : 'var(--fg-muted)',
              background: 'none',
              border: 'none',
              borderBottom: tab === key ? '2px solid var(--accent)' : '2px solid transparent',
              cursor: 'pointer',
              marginBottom: '-1px',
            }}
          >
            <Icon size={13} />
            {label}
          </button>
        ))}
      </div>

      <div className="mt-6">
        {tab === 'deploy' && (
          <DeployTab deployments={deployments} />
        )}
        {tab === 'config' && (
          <ConfigTab
            form={editForm}
            onChange={setEditForm}
            onSave={handleSave}
            onDelete={handleDelete}
            saving={saving}
          />
        )}
        {tab === 'datasets' && (
          <DatasetsTab
            available={availableDatasets}
            selected={projectDatasets}
            onToggle={toggleDataset}
            onUpdateMode={updateMountMode}
          />
        )}
      </div>
    </div>
  )
}

function DeployTab({ deployments }: { deployments: Deployment[] }) {
  const [expanded, setExpanded] = useState<string | null>(deployments[0]?.id ?? null)

  if (deployments.length === 0) {
    return (
      <div className="py-12 text-center" style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
        No deployments yet. Click Deploy to start.
      </div>
    )
  }

  return (
    <div className="space-y-px" style={{ background: 'var(--border)' }}>
      {deployments.map(d => {
        const color = statusColor(d.status)
        const isOpen = expanded === d.id
        return (
          <div key={d.id} style={{ background: 'var(--bg-card)' }}>
            <button
              className="w-full flex items-center gap-4 px-5 py-4 text-left transition-colors"
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'inherit' }}
              onClick={() => setExpanded(isOpen ? null : d.id)}
              onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--bg-hover)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'transparent' }}
            >
              <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: color, flexShrink: 0 }} className={d.status === 'running' ? 'status-running' : ''} />
              <div className="flex-1 min-w-0">
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.8rem', color: 'var(--fg-primary)' }}>
                  {d.commit_sha ? d.commit_sha.slice(0, 12) : d.id.slice(0, 8)}
                  {d.image_tag && (
                    <span style={{ color: 'var(--fg-muted)', marginLeft: '1rem' }}>
                      {d.image_tag.split(':').pop()}
                    </span>
                  )}
                </div>
              </div>
              <div
                className="px-2 py-0.5 rounded-sm text-xs"
                style={{ fontFamily: 'DM Mono', color, border: `1px solid ${color}33`, background: `${color}11`, flexShrink: 0 }}
              >
                {d.status.toUpperCase()}
              </div>
              <div style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
                {timeAgo(d.created_at)}
              </div>
              {isOpen ? <ChevronUp size={14} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} /> : <ChevronDown size={14} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />}
            </button>
            {isOpen && d.logs && (
              <div
                className="terminal-scroll"
                style={{
                  background: '#0a0a0a',
                  borderTop: '1px solid var(--border)',
                  padding: '1rem 1.5rem',
                  maxHeight: '360px',
                  overflowY: 'auto',
                  fontFamily: 'DM Mono',
                  fontSize: '0.72rem',
                  color: '#a8a29e',
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
  )
}

function ConfigTab({ form, onChange, onSave, onDelete, saving }: {
  form: Partial<Project>
  onChange: (f: Partial<Project>) => void
  onSave: () => void
  onDelete: () => void
  saving: boolean
}) {
  const inputStyle = {
    background: 'var(--bg-hover)',
    border: '1px solid var(--border)',
    color: 'var(--fg-primary)',
    fontFamily: 'DM Mono',
    outline: 'none',
  }
  const field = (label: string, key: keyof Project, hint?: string) => (
    <div key={key}>
      <label style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', letterSpacing: '0.1em', display: 'block', marginBottom: '0.4rem' }}>
        {label.toUpperCase()}
      </label>
      <input
        type="text"
        value={(form[key] ?? '') as string}
        onChange={e => onChange({ ...form, [key]: e.target.value })}
        className="w-full px-3 py-2 rounded-sm text-sm"
        style={inputStyle}
        onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
        onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
      />
      {hint && (
        <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
          {hint}
        </p>
      )}
    </div>
  )

  const nameIsValidPrefix = isValidDomainPrefix(form.name ?? '')
  const domainPrefixRequired = !nameIsValidPrefix

  return (
    <div className="max-w-lg space-y-5">
      {field('Project Name', 'name')}
      {field('Git URL', 'git_url')}
      {field('Git Branch', 'git_branch')}

      <div>
        <label style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', letterSpacing: '0.1em', display: 'block', marginBottom: '0.4rem' }}>
          DOMAIN PREFIX
          {domainPrefixRequired && (
            <span style={{ color: 'var(--danger)', marginLeft: '0.3em' }}>*</span>
          )}
        </label>
        <input
          type="text"
          value={(form.domain_prefix ?? '') as string}
          onChange={e => onChange({ ...form, domain_prefix: e.target.value })}
          placeholder={nameIsValidPrefix ? form.name : undefined}
          className="w-full px-3 py-2 rounded-sm text-sm"
          style={inputStyle}
          onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
          onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
        />
        <p style={{ fontFamily: 'DM Mono', fontSize: '0.65rem', marginTop: '0.35rem', color: domainPrefixRequired ? 'var(--danger)' : 'var(--fg-muted)' }}>
          {nameIsValidPrefix
            ? `Optional — defaults to "${form.name}" if left blank`
            : 'Required — project name cannot be used as a subdomain'}
        </p>
      </div>

      {field('Dockerfile Path', 'dockerfile_path')}

      <div>
        <label style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', letterSpacing: '0.1em', display: 'block', marginBottom: '0.4rem' }}>
          REQUIRE GOOGLE AUTH
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
                background: form.auth_required ? '#0f0f0f' : 'var(--fg-muted)',
                left: form.auth_required ? '18px' : '4px',
              }}
            />
          </div>
          <span style={{ fontFamily: 'DM Mono', fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
            {form.auth_required ? 'Enabled' : 'Disabled'}
          </span>
        </label>
      </div>

      {form.auth_required && field('Allowed Email Domains (comma separated)', 'auth_allowed_domains')}

      <div className="flex gap-3 pt-2">
        <button
          onClick={onSave}
          disabled={saving}
          className="px-5 py-2 rounded-sm text-sm transition-all"
          style={{
            background: 'var(--accent)',
            color: '#0f0f0f',
            fontFamily: 'DM Mono',
            border: 'none',
            cursor: saving ? 'not-allowed' : 'pointer',
            opacity: saving ? 0.7 : 1,
          }}
        >
          {saving ? 'Saving...' : 'Save Changes'}
        </button>
        <button
          onClick={onDelete}
          className="flex items-center gap-2 px-4 py-2 rounded-sm text-sm transition-all"
          style={{ background: 'var(--bg-hover)', color: 'var(--danger)', fontFamily: 'DM Mono', border: '1px solid var(--border)', cursor: 'pointer' }}
        >
          <Trash2 size={13} /> Delete
        </button>
      </div>
    </div>
  )
}

function DatasetsTab({ available, selected, onToggle, onUpdateMode }: {
  available: Dataset[]
  selected: ProjectDataset[]
  onToggle: (id: string, mode: 'dependency' | 'readwrite') => void
  onUpdateMode: (id: string, mode: 'dependency' | 'readwrite') => void
}) {
  const selectedMap = Object.fromEntries(selected.map(pd => [pd.dataset_id, pd]))

  return (
    <div>
      <p style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', marginBottom: '1.5rem' }}>
        Select datasets to mount into the container. <span style={{ color: 'var(--accent)' }}>dependency</span> = rsync copy (LRU cached). <span style={{ color: '#3cb8f0' }}>readwrite</span> = direct NFS mount.
      </p>
      <div className="space-y-px" style={{ background: 'var(--border)' }}>
        {available.map(ds => {
          const sel = selectedMap[ds.id]
          return (
            <div
              key={ds.id}
              className="flex items-center gap-4 px-5 py-4"
              style={{ background: sel ? 'var(--bg-hover)' : 'var(--bg-card)' }}
            >
              <input
                type="checkbox"
                checked={!!sel}
                onChange={() => onToggle(ds.id, 'dependency')}
                style={{ accentColor: 'var(--accent)', width: '14px', height: '14px' }}
              />
              <div className="flex-1 min-w-0">
                <div style={{ fontFamily: 'Lora', fontSize: '0.9rem', color: 'var(--fg-primary)' }}>{ds.name}</div>
                <div style={{ fontFamily: 'DM Mono', fontSize: '0.68rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {ds.nfs_path}
                </div>
              </div>
              <div style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
                {formatBytes(ds.size_bytes)} · v{ds.version}
              </div>
              {sel && (
                <select
                  value={sel.mount_mode}
                  onChange={e => onUpdateMode(ds.id, e.target.value as 'dependency' | 'readwrite')}
                  style={{
                    background: 'var(--bg-base)',
                    border: '1px solid var(--border)',
                    color: sel.mount_mode === 'dependency' ? 'var(--accent)' : '#3cb8f0',
                    fontFamily: 'DM Mono',
                    fontSize: '0.7rem',
                    padding: '2px 6px',
                    borderRadius: '2px',
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
          <div className="py-10 text-center" style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
            No datasets available. Create one in the Datasets section.
          </div>
        )}
      </div>
    </div>
  )
}
