import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Lock, ChevronDown, ChevronUp, Eye, EyeOff, GitBranch, Globe, Copy, Check, Radio, Key } from 'lucide-react'
import { api } from '../lib/api'
import type { Project, Secret } from '../lib/types'
import { isValidDomainPrefix } from '../lib/utils'
import { useTranslation } from 'react-i18next'

// ─── Git provider detection ───────────────────────────────────────────────────

function detectProvider(url: string): { credType: 'pat' | 'ssh'; gitUsername: string } {
  const u = url.trim()
  if (u.startsWith('git@') || u.startsWith('ssh://')) {
    return { credType: 'ssh', gitUsername: '' }
  }
  if (u.includes('github.com')) return { credType: 'pat', gitUsername: 'x-access-token' }
  if (u.includes('gitlab.com')) return { credType: 'pat', gitUsername: 'oauth2' }
  if (u.includes('bitbucket.org')) return { credType: 'pat', gitUsername: '' }
  if (u.includes('dev.azure.com') || u.includes('visualstudio.com')) return { credType: 'pat', gitUsername: 'AzureDevOps' }
  return { credType: 'pat', gitUsername: '' }
}

// ─── Styles ───────────────────────────────────────────────────────────────────

const MONO = 'var(--font-mono)'

// ─── Private-repo credential section ─────────────────────────────────────────

type CredMode = 'none' | 'existing' | 'new_pat' | 'new_ssh'

interface CredConfig {
  mode: CredMode
  // existing secret
  existingSecretId: string
  // new PAT
  patGitUsername: string
  patValue: string
  // new SSH key
  sshKeyValue: string
}

function PrivateRepoSection({
  gitUrl,
  projectName,
  cred,
  onChange,
}: {
  gitUrl: string
  projectName: string
  cred: CredConfig
  onChange: (c: CredConfig) => void
}) {
  const [open, setOpen] = useState(false)
  const [secrets, setSecrets] = useState<Secret[]>([])
  const [showPat, setShowPat] = useState(false)
  const { credType, gitUsername } = detectProvider(gitUrl)
  const { t } = useTranslation()

  // Load existing secrets when user opens the panel
  useEffect(() => {
    if (open && secrets.length === 0) {
      api.secrets.list().then(setSecrets).catch(() => {})
    }
  }, [open])

  // When mode changes to 'new_pat', pre-fill the git username
  const setMode = (mode: CredMode) => {
    const next: CredConfig = { ...cred, mode }
    if (mode === 'new_pat' && !next.patGitUsername) {
      next.patGitUsername = gitUsername || 'x-access-token'
    }
    if (mode === 'none') {
      next.existingSecretId = ''
      next.patValue = ''
      next.sshKeyValue = ''
    }
    onChange(next)
  }

  const providerHint = () => {
    const u = gitUrl.toLowerCase()
    if (u.includes('github.com')) return 'GitHub'
    if (u.includes('gitlab.com')) return 'GitLab'
    if (u.includes('bitbucket.org')) return 'Bitbucket'
    if (u.includes('dev.azure.com') || u.includes('visualstudio.com')) return 'Azure DevOps'
    return null
  }

  const provider = providerHint()
  const defaultCredType = credType

  return (
    <div className="card" style={{ overflow: 'hidden' }}>
      {/* Toggle header */}
      <button
        type="button"
        onClick={() => { setOpen(o => !o); if (!open) setMode(cred.mode === 'none' ? (defaultCredType === 'ssh' ? 'new_ssh' : 'new_pat') : cred.mode) }}
        className="w-full flex items-center gap-3 px-4 py-3 text-left"
        style={{ background: 'none', border: 'none', cursor: 'pointer' }}
        onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.background = 'var(--bg-hover)' }}
        onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.background = 'none' }}
      >
        <Lock size={13} style={{ color: cred.mode !== 'none' ? 'var(--accent)' : 'var(--fg-muted)', flexShrink: 0 }} />
        <div className="flex-1">
          <span style={{ fontSize: '0.8125rem', color: cred.mode !== 'none' ? 'var(--accent)' : 'var(--fg-muted)' }}>
            {cred.mode !== 'none' ? t('newProject.privateRepo.configured') : t('newProject.privateRepo.configure')}
          </span>
          {provider && cred.mode === 'none' && (
            <span style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginLeft: '0.5em' }}>
              {t('newProject.privateRepo.detected', { provider })}
            </span>
          )}
        </div>
        {open ? <ChevronUp size={13} style={{ color: 'var(--fg-muted)' }} /> : <ChevronDown size={13} style={{ color: 'var(--fg-muted)' }} />}
      </button>

      {open && (
        <div style={{ borderTop: '1px solid var(--border)', padding: '1rem 1.25rem' }}>
          {/* Mode tabs */}
          <div className="flex gap-1 mb-4" style={{ flexWrap: 'wrap' }}>
            {[
              { id: 'none' as CredMode, label: t('newProject.privateRepo.none') },
              {
                id: (defaultCredType === 'ssh' ? 'new_ssh' : 'new_pat') as CredMode,
                label: defaultCredType === 'ssh'
                  ? t('newProject.privateRepo.newSshKey')
                  : t('newProject.privateRepo.newToken', { provider: provider ?? 'HTTPS' }),
              },
              { id: 'new_pat' as CredMode, label: t('newProject.privateRepo.newPat'), hidden: defaultCredType !== 'ssh' },
              { id: 'new_ssh' as CredMode, label: t('newProject.privateRepo.newSshKey'), hidden: defaultCredType === 'ssh' },
              { id: 'existing' as CredMode, label: t('newProject.privateRepo.existingSecret') },
            ].filter(tab => !tab.hidden).map(tab => (
              <button
                key={tab.id}
                type="button"
                onClick={() => setMode(tab.id)}
                className={cred.mode === tab.id ? 'btn-primary' : 'btn-secondary'}
                style={{
                  fontSize: '0.8125rem',
                  padding: '0.35rem 0.75rem',
                }}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {/* None */}
          {cred.mode === 'none' && (
            <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
              {t('newProject.privateRepo.noCredentials')}
            </p>
          )}

          {/* New PAT */}
          {cred.mode === 'new_pat' && (
            <div className="flex flex-col gap-3">
              <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', lineHeight: 1.6 }}>
                {provider === 'GitHub'
                  ? t('newProject.privateRepo.githubPatHint')
                  : t('newProject.privateRepo.genericPatHint')}
              </p>
              <div>
                <label className="form-label">{t('newProject.privateRepo.gitUsername')}</label>
                <input
                  value={cred.patGitUsername}
                  onChange={e => onChange({ ...cred, patGitUsername: e.target.value })}
                  placeholder="x-access-token"
                  className="form-input"
                />
                <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.3rem' }}>
                  {provider === 'GitHub' && <>{t('newProject.privateRepo.githubUsernameHint')}</>}
                  {provider === 'GitLab' && <>{t('newProject.privateRepo.gitlabUsernameHint')}</>}
                  {!provider && t('newProject.privateRepo.genericUsernameHint')}
                </p>
              </div>
              <div>
                <label className="form-label">
                  {t('newProject.privateRepo.tokenValue')}{' '}
                  <span style={{ color: 'var(--danger)', fontWeight: 400 }}>{t('newProject.privateRepo.writeOnly')}</span>
                </label>
                <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
                  <input
                    type={showPat ? 'text' : 'password'}
                    value={cred.patValue}
                    onChange={e => onChange({ ...cred, patValue: e.target.value })}
                    placeholder={t('newProject.privateRepo.tokenPlaceholder', { provider: provider ?? 'access' })}
                    className="form-input"
                    style={{ paddingRight: '2.5rem' }}
                  />
                  <button
                    type="button"
                    onClick={() => setShowPat(v => !v)}
                    style={{ position: 'absolute', right: '0.5rem', background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)' }}
                  >
                    {showPat ? <EyeOff size={14} /> : <Eye size={14} />}
                  </button>
                </div>
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                {t('newProject.privateRepo.newPatSecretHint', { name: projectName || 'project' })}
              </p>
            </div>
          )}

          {/* New SSH Key */}
          {cred.mode === 'new_ssh' && (
            <div className="flex flex-col gap-3">
              <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', lineHeight: 1.6 }}
                dangerouslySetInnerHTML={{ __html: t('newProject.privateRepo.privateKeyHint') }}
              />
              <div>
                <label className="form-label">
                  {t('newProject.privateRepo.privateKey')}{' '}
                  <span style={{ color: 'var(--danger)', fontWeight: 400 }}>{t('newProject.privateRepo.writeOnly')}</span>
                </label>
                <textarea
                  value={cred.sshKeyValue}
                  onChange={e => onChange({ ...cred, sshKeyValue: e.target.value })}
                  placeholder={'-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----'}
                  rows={6}
                  className="form-input"
                  style={{ resize: 'vertical', fontFamily: MONO, fontSize: '0.8125rem' }}
                />
              </div>
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                {t('newProject.privateRepo.newSshSecretHint', { name: projectName || 'project' })}
              </p>
            </div>
          )}

          {/* Existing secret */}
          {cred.mode === 'existing' && (
            <div className="flex flex-col gap-3">
              <div>
                <label className="form-label">{t('newProject.privateRepo.selectSecret')}</label>
                {secrets.length === 0 ? (
                  <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                    {t('newProject.privateRepo.noSecrets')}{' '}
                    <a href="/secrets" style={{ color: 'var(--accent)' }}>{t('newProject.privateRepo.createOne')}</a>{' '}
                    {t('newProject.privateRepo.noSecretsFirst')}
                  </p>
                ) : (
                  <select
                    value={cred.existingSecretId}
                    onChange={e => onChange({ ...cred, existingSecretId: e.target.value })}
                    className="form-input"
                    style={{ cursor: 'pointer' }}
                  >
                    <option value="">{t('newProject.privateRepo.chooseSecret')}</option>
                    {secrets.map(s => (
                      <option key={s.id} value={s.id}>
                        {s.name} ({s.type === 'ssh_key' ? 'SSH KEY' : 'PASSWORD'})
                      </option>
                    ))}
                  </select>
                )}
              </div>
              {cred.existingSecretId && (() => {
                const sec = secrets.find(s => s.id === cred.existingSecretId)
                if (!sec) return null
                if (sec.type === 'password') return (
                  <div>
                    <label className="form-label">{t('newProject.privateRepo.gitUsername')}</label>
                    <input
                      value={cred.patGitUsername || 'x-access-token'}
                      onChange={e => onChange({ ...cred, patGitUsername: e.target.value })}
                      placeholder="x-access-token"
                      className="form-input"
                    />
                  </div>
                )
                return null
              })()}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Main NewProject component ────────────────────────────────────────────────

export default function NewProject() {
  const [projectType, setProjectType] = useState<'deployment' | 'domain_only'>('deployment')
  const [form, setForm] = useState<Partial<Project>>({
    git_source: 'external',
    git_branch: 'main',
    dockerfile_path: 'Dockerfile',
    auth_required: false,
    auth_allowed_domains: '',
    memory_limit: '4g',
  })
  const [createdProject, setCreatedProject] = useState<Project | null>(null)
  const [copied, setCopied] = useState(false)
  const [generatedToken, setGeneratedToken] = useState<string | null>(null)
  const [generatingToken, setGeneratingToken] = useState(false)
  const [tokenError, setTokenError] = useState('')
  const [showToken, setShowToken] = useState(false)
  const [tokenCopied, setTokenCopied] = useState(false)
  const [cred, setCred] = useState<CredConfig>({
    mode: 'none',
    existingSecretId: '',
    patGitUsername: 'x-access-token',
    patValue: '',
    sshKeyValue: '',
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const navigate = useNavigate()
  const { t } = useTranslation()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    // Validate credential inputs (only for deployment + external repos)
    if (projectType === 'deployment' && form.git_source !== 'hosted') {
      if (cred.mode === 'new_pat' && !cred.patValue.trim()) {
        setError(t('newProject.errors.noToken'))
        return
      }
      if (cred.mode === 'new_ssh' && !cred.sshKeyValue.trim()) {
        setError(t('newProject.errors.noSshKey'))
        return
      }
      if (cred.mode === 'existing' && !cred.existingSecretId) {
        setError(t('newProject.errors.noSecret'))
        return
      }
    }

    setSaving(true)
    try {
      // Step 1: create the project
      const payload: Partial<Project> = projectType === 'domain_only'
        ? { name: form.name, domain_prefix: form.domain_prefix, project_type: 'domain_only' }
        : { ...form, project_type: 'deployment' }
      const project = await api.projects.create(payload)

      // domain_only projects — no credentials, just navigate
      if (projectType === 'domain_only') {
        navigate(`/projects/${project.id}`)
        return
      }

      // For hosted repos, show the push URL instead of navigating
      if (project.git_source === 'hosted' && project.git_push_url) {
        setCreatedProject(project)
        setSaving(false)
        return
      }

      // Step 2: create + bind credential (if any, for external repos)
      if (cred.mode === 'new_pat') {
        const secretName = `${form.name || 'project'} Git Token`
        const sec = await api.secrets.create({ name: secretName, type: 'password', value: cred.patValue.trim() })
        await api.projects.setSecrets(project.id, [{
          secret_id: sec.id,
          env_var_name: '',
          use_for_git: true,
          use_for_build: false,
          build_secret_id: '',
          git_username: cred.patGitUsername || 'x-access-token',
        }])
      } else if (cred.mode === 'new_ssh') {
        const secretName = `${form.name || 'project'} Deploy Key`
        const sec = await api.secrets.create({ name: secretName, type: 'ssh_key', value: cred.sshKeyValue.trim() })
        await api.projects.setSecrets(project.id, [{
          secret_id: sec.id,
          env_var_name: '',
          use_for_git: true,
          use_for_build: false,
          build_secret_id: '',
          git_username: '',
        }])
      } else if (cred.mode === 'existing' && cred.existingSecretId) {
        const allSecrets = await api.secrets.list()
        const sec = allSecrets.find(s => s.id === cred.existingSecretId)
        await api.projects.setSecrets(project.id, [{
          secret_id: cred.existingSecretId,
          env_var_name: '',
          use_for_git: true,
          use_for_build: false,
          build_secret_id: '',
          git_username: sec?.type === 'password' ? (cred.patGitUsername || 'x-access-token') : '',
        }])
      }

      navigate(`/projects/${project.id}`)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const nameIsValidPrefix = isValidDomainPrefix(form.name ?? '')
  const domainPrefixRequired = !nameIsValidPrefix

  const fieldLabel = (text: string, required = true) => (
    <label className="form-label">
      {text.toUpperCase()}
      {required && <span style={{ color: 'var(--danger)', marginLeft: '0.3em' }}>*</span>}
    </label>
  )

  // If a hosted project was just created, show the push URL
  if (createdProject && createdProject.git_push_url) {
    const pushUrl = createdProject.git_push_url
    const urlWithToken = generatedToken
      ? pushUrl.replace(/^(https?:\/\/)/, (_m, scheme) => `${scheme}x:${generatedToken}@`)
      : pushUrl
    const copyUrl = () => {
      navigator.clipboard.writeText(urlWithToken)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
    const copyToken = () => {
      if (!generatedToken) return
      navigator.clipboard.writeText(generatedToken)
      setTokenCopied(true)
      setTimeout(() => setTokenCopied(false), 2000)
    }
    const generateToken = async () => {
      if (!createdProject) return
      setGeneratingToken(true)
      setTokenError('')
      try {
        const result = await api.tokens.create(createdProject.id, t('newProject.hostedCreated.tokenName'))
        setGeneratedToken(result.token)
        setShowToken(true)
      } catch (err) {
        setTokenError((err as Error).message)
      } finally {
        setGeneratingToken(false)
      }
    }
    return (
      <div className="page-enter" style={{ maxWidth: '520px' }}>
        <div className="page-header">
          <p className="page-subtitle">{t('newProject.sectionLabel')}</p>
          <h1 className="page-title">{t('newProject.hostedCreated.heading')}</h1>
        </div>
        <div className="flex flex-col gap-5">
          <p style={{ fontSize: '0.875rem', color: 'var(--fg-secondary)', lineHeight: 1.6 }}>
            {t('newProject.hostedCreated.description', { name: createdProject.name })}
          </p>

          {/* Token generation section */}
          {!generatedToken ? (
            <div className="card" style={{ padding: '0.875rem 1rem', borderColor: 'var(--border)' }}>
              <div className="flex items-center gap-2" style={{ marginBottom: '0.5rem' }}>
                <Key size={14} style={{ color: 'var(--accent)' }} />
                <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)' }}>
                  {t('newProject.hostedCreated.tokenPendingHeading')}
                </span>
              </div>
              <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', lineHeight: 1.6, marginBottom: '0.75rem' }}>
                {t('newProject.hostedCreated.tokenPendingHint')}
              </p>
              <button
                type="button"
                onClick={generateToken}
                disabled={generatingToken}
                className="btn-primary flex items-center gap-2"
                style={{ fontSize: '0.8125rem', padding: '0.4rem 0.9rem' }}
              >
                <Key size={13} />
                {generatingToken ? t('newProject.hostedCreated.generating') : t('newProject.hostedCreated.generateToken')}
              </button>
              {tokenError && (
                <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginTop: '0.5rem' }}>{tokenError}</p>
              )}
            </div>
          ) : (
            <div className="card" style={{
              background: 'rgba(37,99,235,0.08)', borderColor: 'rgba(37,99,235,0.3)',
              padding: '0.75rem 1rem',
            }}>
              <p style={{ fontSize: '0.8125rem', color: 'var(--accent)', marginBottom: '0.4rem', fontWeight: 600 }}>
                {t('newProject.hostedCreated.tokenReadyHeading')}
              </p>
              <div className="flex items-center gap-2">
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
                <button
                  type="button"
                  onClick={copyToken}
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

          <div>
            <label className="form-label">{t('newProject.hostedCreated.pushUrl')}</label>
            <div className="flex items-center gap-2">
              <code style={{
                flex: 1, fontFamily: MONO, fontSize: '0.875rem', padding: '0.5rem 0.75rem',
                background: 'var(--bg-hover)', border: '1px solid var(--border)', borderRadius: '6px',
                color: 'var(--accent)', wordBreak: 'break-all',
              }}>{urlWithToken}</code>
              <button type="button" onClick={copyUrl} className="btn-secondary" style={{ padding: '0.5rem' }}>
                {copied ? <Check size={14} style={{ color: 'var(--accent)' }} /> : <Copy size={14} />}
              </button>
            </div>
          </div>
          <div style={{ fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-muted)', lineHeight: 1.8, background: 'var(--bg-hover)', padding: '0.75rem 1rem', borderRadius: '6px' }}>
            <div>git remote add muvee {urlWithToken}</div>
            <div>git push muvee main</div>
          </div>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
            {generatedToken
              ? t('newProject.hostedCreated.authHintWithToken')
              : t('newProject.hostedCreated.authHint')}
          </p>
          <button
            onClick={() => navigate(`/projects/${createdProject.id}`)}
            className="btn-primary"
            style={{ alignSelf: 'flex-start' }}
          >
            {t('newProject.hostedCreated.goToProject')}
          </button>
        </div>
      </div>
    )
  }

  const isHosted = form.git_source === 'hosted'

  return (
    <div className="page-enter" style={{ maxWidth: '520px' }}>
        <div className="page-header">
          <p className="page-subtitle">{t('newProject.sectionLabel')}</p>
          <h1 className="page-title">{t('newProject.heading')}</h1>
        </div>

        <form onSubmit={handleSubmit} className="flex flex-col gap-5">
          {/* Project Type selector */}
          <div>
            <label className="form-label">{t('newProject.fields.projectType').toUpperCase()}</label>
            <div className="flex gap-2">
              {([
                { id: 'deployment' as const, icon: Globe, label: t('newProject.projectType.deployment') },
                { id: 'domain_only' as const, icon: Radio, label: t('newProject.projectType.tunnel') },
              ]).map(opt => (
                <button
                  key={opt.id}
                  type="button"
                  onClick={() => setProjectType(opt.id)}
                  className="flex items-center gap-2 flex-1"
                  style={{
                    fontSize: '0.875rem', padding: '0.55rem 0.75rem',
                    borderRadius: '6px', cursor: 'pointer',
                    border: `1px solid ${projectType === opt.id ? 'var(--accent)' : 'var(--border)'}`,
                    background: projectType === opt.id ? 'rgba(37,99,235,0.08)' : 'var(--bg-hover)',
                    color: projectType === opt.id ? 'var(--accent)' : 'var(--fg-muted)',
                  }}
                >
                  <opt.icon size={14} />
                  {opt.label}
                </button>
              ))}
            </div>
            {projectType === 'domain_only' && (
              <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                {t('newProject.projectType.tunnelHint')}
              </p>
            )}
          </div>

          {/* Git Source selector — deployment only */}
          {projectType === 'deployment' && (
          <div>
            <label className="form-label">{t('newProject.fields.gitSource').toUpperCase()}</label>
            <div className="flex gap-2">
              {([
                { id: 'external' as const, icon: Globe, label: t('newProject.gitSource.external') },
                { id: 'hosted' as const, icon: GitBranch, label: t('newProject.gitSource.hosted') },
              ]).map(opt => (
                <button
                  key={opt.id}
                  type="button"
                  onClick={() => setForm({ ...form, git_source: opt.id })}
                  className="flex items-center gap-2 flex-1"
                  style={{
                    fontSize: '0.875rem', padding: '0.55rem 0.75rem',
                    borderRadius: '6px', cursor: 'pointer',
                    border: `1px solid ${form.git_source === opt.id ? 'var(--accent)' : 'var(--border)'}`,
                    background: form.git_source === opt.id ? 'rgba(37,99,235,0.08)' : 'var(--bg-hover)',
                    color: form.git_source === opt.id ? 'var(--accent)' : 'var(--fg-muted)',
                  }}
                >
                  <opt.icon size={14} />
                  {opt.label}
                </button>
              ))}
            </div>
            {isHosted && (
              <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
                {t('newProject.gitSource.hostedHint')}
              </p>
            )}
          </div>
          )}

          {/* Name */}
          <div>
            {fieldLabel(t('newProject.fields.name'))}
            <input
              value={form.name ?? ''}
              onChange={e => setForm({ ...form, name: e.target.value })}
              required
              className="form-input"
            />
          </div>

          {/* Git URL — only for deployment + external repos */}
          {projectType === 'deployment' && !isHosted && (
            <div>
              {fieldLabel(t('newProject.fields.gitUrl'))}
              <input
                value={form.git_url ?? ''}
                onChange={e => setForm({ ...form, git_url: e.target.value })}
                required
                placeholder="https://github.com/owner/repo.git"
                className="form-input"
              />
            </div>
          )}

          {/* Private repo credential shortcut — only for deployment + external repos */}
          {projectType === 'deployment' && !isHosted && (form.git_url ?? '').trim() !== '' && (
            <PrivateRepoSection
              gitUrl={form.git_url ?? ''}
              projectName={form.name ?? ''}
              cred={cred}
              onChange={setCred}
            />
          )}

          {/* Branch — only for deployment + external repos */}
          {projectType === 'deployment' && !isHosted && (
            <div>
              {fieldLabel(t('newProject.fields.gitBranch'), false)}
              <input
                value={form.git_branch ?? ''}
                onChange={e => setForm({ ...form, git_branch: e.target.value })}
                placeholder="main"
                className="form-input"
              />
            </div>
          )}

          {/* Domain prefix — required for domain_only, optional for deployment */}
          <div>
            <label className="form-label">
              {t('newProject.fields.domainPrefix').toUpperCase()}
              {(projectType === 'domain_only' || domainPrefixRequired) && <span style={{ color: 'var(--danger)', marginLeft: '0.3em' }}>*</span>}
            </label>
            <input
              value={form.domain_prefix ?? ''}
              onChange={e => setForm({ ...form, domain_prefix: e.target.value })}
              required={projectType === 'domain_only' || domainPrefixRequired}
              placeholder={nameIsValidPrefix ? form.name : undefined}
              className="form-input"
            />
            <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: (projectType === 'domain_only' || domainPrefixRequired) ? 'var(--danger)' : 'var(--fg-muted)' }}>
              {projectType === 'domain_only'
                ? t('newProject.domainTunnelHint')
                : nameIsValidPrefix
                  ? t('newProject.domainOptional', { name: form.name })
                  : t('newProject.domainRequired')}
            </p>
          </div>

          {/* Description */}
          <div>
            {fieldLabel(t('newProject.fields.description'), false)}
            <input
              value={form.description ?? ''}
              onChange={e => setForm({ ...form, description: e.target.value })}
              placeholder={t('newProject.fields.descriptionPlaceholder')}
              className="form-input"
            />
          </div>

          {/* Tags */}
          <div>
            {fieldLabel(t('newProject.fields.tags'), false)}
            <input
              value={form.tags ?? ''}
              onChange={e => setForm({ ...form, tags: e.target.value })}
              placeholder={t('newProject.fields.tagsPlaceholder')}
              className="form-input"
            />
            <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('newProject.fields.tagsHint')}
            </p>
          </div>

          {/* Icon */}
          <div>
            {fieldLabel(t('newProject.fields.icon'), false)}
            <textarea
              value={form.icon ?? ''}
              onChange={e => setForm({ ...form, icon: e.target.value })}
              placeholder={t('newProject.fields.iconPlaceholder')}
              rows={3}
              className="form-input"
              style={{ resize: 'vertical', fontFamily: MONO, fontSize: '0.8125rem' }}
            />
            <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('newProject.fields.iconHint')}
            </p>
          </div>

          {/* Dockerfile path — deployment only */}
          {projectType === 'deployment' && (
          <div>
            {fieldLabel(t('newProject.fields.dockerfilePath'), false)}
            <input
              value={form.dockerfile_path ?? ''}
              onChange={e => setForm({ ...form, dockerfile_path: e.target.value })}
              placeholder="Dockerfile"
              className="form-input"
            />
          </div>
          )}

          {/* Memory limit — deployment only */}
          {projectType === 'deployment' && (
          <div>
            {fieldLabel(t('newProject.fields.memoryLimit'), false)}
            <input
              value={form.memory_limit ?? ''}
              onChange={e => setForm({ ...form, memory_limit: e.target.value })}
              placeholder="4g"
              className="form-input"
            />
            <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('newProject.fields.memoryLimitHint')}
            </p>
          </div>
          )}

          {/* Persistent storage path — deployment only */}
          {projectType === 'deployment' && (
          <div>
            {fieldLabel(t('newProject.fields.volumeMountPath'), false)}
            <input
              value={form.volume_mount_path ?? ''}
              onChange={e => setForm({ ...form, volume_mount_path: e.target.value })}
              placeholder="/workspace"
              className="form-input"
            />
            <p style={{ fontSize: '0.75rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('newProject.fields.volumeMountPathHint')}
            </p>
          </div>
          )}

          {error && (
            <p style={{ fontSize: '0.875rem', color: 'var(--danger)' }}>{error}</p>
          )}

          <button
            type="submit"
            disabled={saving}
            className="btn-primary"
            style={{
              alignSelf: 'flex-start',
              cursor: saving ? 'not-allowed' : 'pointer',
              opacity: saving ? 0.7 : 1,
            }}
          >
            {saving ? t('newProject.creating') : t('newProject.createProject')}
          </button>
        </form>
    </div>
  )
}
