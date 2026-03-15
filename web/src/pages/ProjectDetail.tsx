import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Rocket, Settings, Database, KeyRound, ChevronDown, ChevronUp, Trash2, ArrowLeft, Link2, Link2Off, ExternalLink } from 'lucide-react'
import { api } from '../lib/api'
import type { Dataset, Deployment, Project, ProjectDataset, ProjectSecretBinding, Secret } from '../lib/types'
import { statusColor, timeAgo, formatBytes, isValidDomainPrefix } from '../lib/utils'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

type Tab = 'deploy' | 'config' | 'datasets' | 'secrets'

export default function ProjectDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [project, setProject] = useState<Project | null>(null)
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [datasets, setDatasets] = useState<Dataset[]>([])
  const [projectDatasets, setProjectDatasets] = useState<ProjectDataset[]>([])
  const [availableDatasets, setAvailableDatasets] = useState<Dataset[]>([])
  const [projectSecrets, setProjectSecrets] = useState<ProjectSecretBinding[]>([])
  const [allSecrets, setAllSecrets] = useState<Secret[]>([])
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
    api.projects.secrets(id).then(setProjectSecrets)
    api.secrets.list().then(setAllSecrets)
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
    if (!id || !confirm(t('projectDetail.config.deleteConfirm'))) return
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

  if (!project) return <div style={{ fontFamily: MONO, color: 'var(--fg-muted)', padding: '2rem' }}>{t('projects.loading')}</div>

  const latestDeploy = deployments[0]
  const color = statusColor(latestDeploy?.status ?? 'pending')

  return (
    <div className="page-enter">
      {/* Header */}
      <div className="mb-8">
        <button
          onClick={() => navigate('/projects')}
          className="flex items-center gap-1 mb-4 text-xs transition-colors"
          style={{ fontFamily: MONO, color: 'var(--fg-muted)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
        >
          <ArrowLeft size={12} /> {t('projectDetail.backToProjects')}
        </button>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div
              className="w-2.5 h-2.5 rounded-full"
              style={{ background: color, flexShrink: 0 }}
            />
            <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2 }}>
              {project.name}
            </h1>
          </div>
          <div className="flex items-center gap-2">
            <a
              href={`https://${project.domain_prefix}.domain.com`}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2 px-4 py-2 rounded-md text-sm transition-all duration-150"
              style={{
                background: 'var(--bg-hover)',
                color: 'var(--fg-muted)',
                fontWeight: 500,
                border: '1px solid var(--border)',
                cursor: 'pointer',
                textDecoration: 'none',
              }}
              onMouseEnter={e => {
                (e.currentTarget as HTMLAnchorElement).style.color = 'var(--fg-primary)'
                ;(e.currentTarget as HTMLAnchorElement).style.borderColor = 'var(--fg-muted)'
              }}
              onMouseLeave={e => {
                (e.currentTarget as HTMLAnchorElement).style.color = 'var(--fg-muted)'
                ;(e.currentTarget as HTMLAnchorElement).style.borderColor = 'var(--border)'
              }}
            >
              <ExternalLink size={14} />
              {t('projectDetail.visit')}
            </a>
            <button
              onClick={handleDeploy}
              disabled={deploying}
              className="flex items-center gap-2 px-4 py-2 rounded-md text-sm transition-all duration-150"
              style={{
                background: deploying ? 'var(--bg-hover)' : 'var(--accent)',
                color: deploying ? 'var(--fg-muted)' : '#ffffff',
                fontWeight: 500,
                border: 'none',
                cursor: deploying ? 'not-allowed' : 'pointer',
              }}
            >
              <Rocket size={14} />
              {deploying ? t('projectDetail.triggering') : t('projectDetail.deploy')}
            </button>
          </div>
        </div>
        <div style={{ fontFamily: MONO, fontSize: '0.78rem', color: 'var(--fg-muted)', marginTop: '0.4rem', marginLeft: '1.75rem' }}>
          {project.domain_prefix}.domain.com
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-0 mb-0" style={{ borderBottom: '1px solid var(--border)' }}>
        {([
          ['deploy', Rocket, t('projectDetail.tabs.deployments')],
          ['config', Settings, t('projectDetail.tabs.config')],
          ['datasets', Database, t('projectDetail.tabs.datasets')],
          ['secrets', KeyRound, t('projectDetail.tabs.secrets')],
        ] as const).map(([key, Icon, label]) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className="flex items-center gap-2 px-4 py-2.5 text-sm transition-all duration-150"
            style={{
              fontFamily: MONO,
              color: tab === key ? 'var(--fg-primary)' : 'var(--fg-muted)',
              background: 'none',
              border: 'none',
              borderBottom: tab === key ? '2px solid var(--accent)' : '2px solid transparent',
              cursor: 'pointer',
              marginBottom: '-1px',
              fontWeight: tab === key ? 500 : 400,
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
        {tab === 'secrets' && id && (
          <SecretsTab
            projectId={id}
            allSecrets={allSecrets}
            bindings={projectSecrets}
            onBindingsChange={setProjectSecrets}
          />
        )}
      </div>
    </div>
  )
}

function DeployTab({ deployments }: { deployments: Deployment[] }) {
  const [expanded, setExpanded] = useState<string | null>(deployments[0]?.id ?? null)
  const { t } = useTranslation()

  if (deployments.length === 0) {
    return (
      <div className="py-12 text-center" style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
        {t('projectDetail.noDeployments')}
      </div>
    )
  }

  return (
    <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
      {deployments.map((d, i) => {
        const color = statusColor(d.status)
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
              <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: color, flexShrink: 0 }} className={d.status === 'running' ? 'status-running' : ''} />
              <div className="flex-1 min-w-0">
                <div style={{ fontFamily: MONO, fontSize: '0.82rem', color: 'var(--fg-primary)' }}>
                  {d.commit_sha ? d.commit_sha.slice(0, 12) : d.id.slice(0, 8)}
                  {d.image_tag && (
                    <span style={{ color: 'var(--fg-muted)', marginLeft: '1rem' }}>
                      {d.image_tag.split(':').pop()}
                    </span>
                  )}
                </div>
              </div>
              <div
                className="px-2 py-0.5 rounded-full text-xs"
                style={{ fontFamily: MONO, color, border: `1px solid ${color}44`, background: `${color}18`, flexShrink: 0 }}
              >
                {d.status}
              </div>
              {d.oom_killed && (
                <div
                  className="px-2 py-0.5 rounded-full text-xs"
                  style={{ fontFamily: MONO, color: '#ff6b6b', border: '1px solid #ff6b6b44', background: '#ff6b6b18', flexShrink: 0 }}
                  title="容器曾因 OOM 被杀死"
                >
                  OOM
                </div>
              )}
              {d.restart_count > 0 && (
                <div
                  className="px-2 py-0.5 rounded-full text-xs"
                  style={{ fontFamily: MONO, color: '#ffa94d', border: '1px solid #ffa94d44', background: '#ffa94d18', flexShrink: 0 }}
                  title={`容器已重启 ${d.restart_count} 次`}
                >
                  ↺{d.restart_count}
                </div>
              )}
              <div style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
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
                  fontSize: '0.75rem',
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
  )
}

function ConfigTab({ form, onChange, onSave, onDelete, saving }: {
  form: Partial<Project>
  onChange: (f: Partial<Project>) => void
  onSave: () => void
  onDelete: () => void
  saving: boolean
}) {
  const { t } = useTranslation()
  const inputStyle = {
    background: 'var(--bg-hover)',
    border: '1px solid var(--border)',
    color: 'var(--fg-primary)',
    fontFamily: MONO,
    outline: 'none',
    borderRadius: '6px',
    fontSize: '0.875rem',
  }
  const field = (label: string, key: keyof Project, hint?: string) => (
    <div key={key}>
      <label style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', letterSpacing: '0.05em', display: 'block', marginBottom: '0.4rem' }}>
        {label.toUpperCase()}
      </label>
      <input
        type="text"
        value={(form[key] ?? '') as string}
        onChange={e => onChange({ ...form, [key]: e.target.value })}
        className="w-full px-3 py-2"
        style={inputStyle}
        onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
        onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
      />
      {hint && (
        <p style={{ fontFamily: MONO, fontSize: '0.68rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
          {hint}
        </p>
      )}
    </div>
  )

  const nameIsValidPrefix = isValidDomainPrefix(form.name ?? '')
  const domainPrefixRequired = !nameIsValidPrefix

  return (
    <div className="max-w-lg space-y-5">
      {field(t('projectDetail.config.projectName'), 'name')}
      {field(t('projectDetail.config.gitUrl'), 'git_url')}
      {field(t('projectDetail.config.gitBranch'), 'git_branch')}

      <div>
        <label style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', letterSpacing: '0.05em', display: 'block', marginBottom: '0.4rem' }}>
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
          className="w-full px-3 py-2"
          style={inputStyle}
          onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
          onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
        />
        <p style={{ fontFamily: MONO, fontSize: '0.68rem', marginTop: '0.35rem', color: domainPrefixRequired ? 'var(--danger)' : 'var(--fg-muted)' }}>
          {nameIsValidPrefix
            ? t('projectDetail.config.domainOptional', { name: form.name })
            : t('projectDetail.config.domainRequired')}
        </p>
      </div>

      {field(t('projectDetail.config.dockerfilePath'), 'dockerfile_path')}
      {field(t('projectDetail.config.memoryLimit'), 'memory_limit', t('projectDetail.config.memoryLimitHint'))}

      <div>
        <label style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', letterSpacing: '0.05em', display: 'block', marginBottom: '0.4rem' }}>
          {t('projectDetail.config.requireGoogleAuth')}
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
          <span style={{ fontFamily: MONO, fontSize: '0.8rem', color: 'var(--fg-muted)' }}>
            {form.auth_required ? t('projectDetail.config.enabled') : t('projectDetail.config.disabled')}
          </span>
        </label>
      </div>

      {form.auth_required && field(t('projectDetail.config.allowedDomains'), 'auth_allowed_domains')}

      <div className="flex gap-3 pt-2">
        <button
          onClick={onSave}
          disabled={saving}
          className="px-4 py-2 rounded-md text-sm transition-all"
          style={{
            background: 'var(--accent)',
            color: '#ffffff',
            fontWeight: 500,
            border: 'none',
            cursor: saving ? 'not-allowed' : 'pointer',
            opacity: saving ? 0.7 : 1,
          }}
        >
          {saving ? t('projectDetail.config.saving') : t('projectDetail.config.saveChanges')}
        </button>
        <button
          onClick={onDelete}
          className="flex items-center gap-2 px-4 py-2 rounded-md text-sm transition-all"
          style={{ background: 'var(--bg-hover)', color: 'var(--danger)', border: '1px solid var(--border)', cursor: 'pointer' }}
        >
          <Trash2 size={13} /> {t('projectDetail.config.delete')}
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
  const { t } = useTranslation()
  const selectedMap = Object.fromEntries(selected.map(pd => [pd.dataset_id, pd]))

  return (
    <div>
      <p style={{ fontFamily: MONO, fontSize: '0.78rem', color: 'var(--fg-muted)', marginBottom: '1.5rem', lineHeight: 1.6 }}>
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
                <span style={{ color: '#79c0ff' }}>readwrite</span>
              </span>
            ) : p
          )
        )}
      </p>
      <div style={{ border: '1px solid var(--border)', borderRadius: '6px', overflow: 'hidden' }}>
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
                <div style={{ fontSize: '0.9rem', fontWeight: 500, color: 'var(--fg-primary)' }}>{ds.name}</div>
                <div style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginTop: '2px' }}>
                  {ds.nfs_path}
                </div>
              </div>
              <div style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', flexShrink: 0 }}>
                {formatBytes(ds.size_bytes)} · v{ds.version}
              </div>
              {sel && (
                <select
                  value={sel.mount_mode}
                  onChange={e => onUpdateMode(ds.id, e.target.value as 'dependency' | 'readwrite')}
                  style={{
                    background: 'var(--bg-base)',
                    border: '1px solid var(--border)',
                    color: sel.mount_mode === 'dependency' ? 'var(--accent)' : '#79c0ff',
                    fontFamily: MONO,
                    fontSize: '0.72rem',
                    padding: '2px 6px',
                    borderRadius: '4px',
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
          <div className="py-10 text-center" style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem', background: 'var(--bg-card)' }}>
            {t('projectDetail.datasets.empty')}
          </div>
        )}
      </div>
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
          git_username: sec.type === 'password' ? 'x-access-token' : '',
        },
      ])
    }
  }

  const updateField = (secretId: string, patch: Partial<ProjectSecretBinding>) => {
    save(bindings.map(b => b.secret_id === secretId ? { ...b, ...patch } : b))
  }

  const mono = 'DM Mono'

  return (
    <div>
      <div className="mb-4" style={{ fontFamily: mono, fontSize: '0.72rem', color: 'var(--fg-muted)' }}>
        {t('projectDetail.secrets.hint')}
        {saving && <span style={{ marginLeft: '1em', color: 'var(--accent)' }}>{t('projectDetail.secrets.saving')}</span>}
      </div>

      <div style={{ border: '1px solid var(--border)', background: 'var(--bg-card)' }}>
        {allSecrets.length === 0 ? (
          <div className="py-12 text-center" style={{ fontFamily: mono, fontSize: '0.8rem', color: 'var(--fg-muted)' }}>
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
                      <span style={{ fontFamily: mono, fontSize: '0.85rem', color: 'var(--fg-primary)' }}>
                        {sec.name}
                      </span>
                      <span style={{
                        fontFamily: mono, fontSize: '0.6rem', padding: '1px 6px', borderRadius: '2px',
                        background: sec.type === 'ssh_key' ? 'rgba(200,240,60,0.15)' : 'rgba(123,97,255,0.2)',
                        color: sec.type === 'ssh_key' ? 'var(--accent)' : '#a78bfa',
                      }}>
                        {sec.type === 'ssh_key' ? t('secrets.sshKey') : t('secrets.password')}
                      </span>
                    </div>

                    {/* Binding options — only shown when bound */}
                    {bound && binding && (
                      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 mt-2">

                        {/* Env var name (all types) */}
                        <div className="flex items-center gap-2">
                          <span style={{ fontFamily: mono, fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.08em' }}>
                            {t('projectDetail.secrets.envVar')}
                          </span>
                          <input
                            value={binding.env_var_name}
                            onChange={e => updateField(sec.id, { env_var_name: e.target.value })}
                            placeholder="MY_SECRET"
                            style={{
                              background: 'var(--bg-base)', border: '1px solid var(--border)',
                              color: 'var(--fg-primary)', fontFamily: mono, fontSize: '0.8rem',
                              padding: '2px 8px', borderRadius: '2px', outline: 'none', width: '180px',
                            }}
                            onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
                            onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
                          />
                        </div>

                        {/* SSH key: use for git clone */}
                        {sec.type === 'ssh_key' && (
                          <label className="flex items-center gap-2" style={{ cursor: 'pointer', fontFamily: mono, fontSize: '0.7rem', color: 'var(--fg-muted)' }}>
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
                            <label className="flex items-center gap-2" style={{ cursor: 'pointer', fontFamily: mono, fontSize: '0.7rem', color: 'var(--fg-muted)' }}>
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
                                <span style={{ fontFamily: mono, fontSize: '0.65rem', color: 'var(--fg-muted)', letterSpacing: '0.08em' }}>
                                  {t('projectDetail.secrets.username')}
                                </span>
                                <input
                                  value={binding.git_username}
                                  onChange={e => updateField(sec.id, { git_username: e.target.value })}
                                  placeholder="x-access-token"
                                  style={{
                                    background: 'var(--bg-base)', border: '1px solid var(--border)',
                                    color: 'var(--fg-primary)', fontFamily: mono, fontSize: '0.8rem',
                                    padding: '2px 8px', borderRadius: '2px', outline: 'none', width: '160px',
                                  }}
                                  onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
                                  onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
                                />
                                <span style={{ fontFamily: mono, fontSize: '0.65rem', color: 'var(--fg-muted)' }}>
                                  {t('projectDetail.secrets.githubPat')} (<code style={{ color: 'var(--accent)' }}>x-access-token</code>)
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
