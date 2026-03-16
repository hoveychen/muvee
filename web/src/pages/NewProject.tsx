import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Lock, ChevronDown, ChevronUp, Eye, EyeOff } from 'lucide-react'
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

const MONO = 'DM Mono'

const inputStyle: React.CSSProperties = {
  width: '100%',
  background: 'var(--bg-hover)',
  border: '1px solid var(--border)',
  color: 'var(--fg-primary)',
  fontFamily: MONO,
  fontSize: '0.85rem',
  padding: '0.5rem 0.75rem',
  borderRadius: '2px',
  outline: 'none',
}

const labelStyle: React.CSSProperties = {
  fontFamily: MONO,
  fontSize: '0.65rem',
  color: 'var(--fg-muted)',
  letterSpacing: '0.1em',
  display: 'block',
  marginBottom: '0.4rem',
}

function focusAccent(e: React.FocusEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) {
  e.target.style.borderColor = 'var(--accent)'
}
function blurBorder(e: React.FocusEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) {
  e.target.style.borderColor = 'var(--border)'
}

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
    <div style={{ border: '1px solid var(--border)', borderRadius: '2px' }}>
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
          <span style={{ fontFamily: MONO, fontSize: '0.78rem', color: cred.mode !== 'none' ? 'var(--accent)' : 'var(--fg-muted)' }}>
            {cred.mode !== 'none' ? t('newProject.privateRepo.configured') : t('newProject.privateRepo.configure')}
          </span>
          {provider && cred.mode === 'none' && (
            <span style={{ fontFamily: MONO, fontSize: '0.65rem', color: 'var(--fg-muted)', marginLeft: '0.5em' }}>
              {t('newProject.privateRepo.detected', { provider })}
            </span>
          )}
        </div>
        {open ? <ChevronUp size={13} style={{ color: 'var(--fg-muted)' }} /> : <ChevronDown size={13} style={{ color: 'var(--fg-muted)' }} />}
      </button>

      {open && (
        <div style={{ borderTop: '1px solid var(--border)', padding: '1rem 1.25rem' }}>
          {/* Mode tabs */}
          <div className="flex gap-1 mb-4">
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
                style={{
                  fontFamily: MONO,
                  fontSize: '0.7rem',
                  padding: '0.3rem 0.75rem',
                  borderRadius: '2px',
                  border: `1px solid ${cred.mode === tab.id ? 'var(--accent)' : 'var(--border)'}`,
                  background: cred.mode === tab.id ? 'rgba(200,240,60,0.1)' : 'var(--bg-hover)',
                  color: cred.mode === tab.id ? 'var(--accent)' : 'var(--fg-muted)',
                  cursor: 'pointer',
                }}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {/* None */}
          {cred.mode === 'none' && (
            <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)' }}>
              {t('newProject.privateRepo.noCredentials')}
            </p>
          )}

          {/* New PAT */}
          {cred.mode === 'new_pat' && (
            <div className="flex flex-col gap-3">
              <p style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', lineHeight: 1.6 }}>
                {provider === 'GitHub'
                  ? t('newProject.privateRepo.githubPatHint')
                  : t('newProject.privateRepo.genericPatHint')}
              </p>
              <div>
                <label style={labelStyle}>{t('newProject.privateRepo.gitUsername')}</label>
                <input
                  value={cred.patGitUsername}
                  onChange={e => onChange({ ...cred, patGitUsername: e.target.value })}
                  placeholder="x-access-token"
                  style={inputStyle}
                  onFocus={focusAccent}
                  onBlur={blurBorder}
                />
                <p style={{ fontFamily: MONO, fontSize: '0.63rem', color: 'var(--fg-muted)', marginTop: '0.3rem' }}>
                  {provider === 'GitHub' && <>{t('newProject.privateRepo.githubUsernameHint')}</>}
                  {provider === 'GitLab' && <>{t('newProject.privateRepo.gitlabUsernameHint')}</>}
                  {!provider && t('newProject.privateRepo.genericUsernameHint')}
                </p>
              </div>
              <div>
                <label style={labelStyle}>
                  {t('newProject.privateRepo.tokenValue')}{' '}
                  <span style={{ color: 'var(--danger)', fontWeight: 400 }}>{t('newProject.privateRepo.writeOnly')}</span>
                </label>
                <div style={{ position: 'relative', display: 'flex', alignItems: 'center' }}>
                  <input
                    type={showPat ? 'text' : 'password'}
                    value={cred.patValue}
                    onChange={e => onChange({ ...cred, patValue: e.target.value })}
                    placeholder={t('newProject.privateRepo.tokenPlaceholder', { provider: provider ?? 'access' })}
                    style={{ ...inputStyle, paddingRight: '2.5rem' }}
                    onFocus={focusAccent}
                    onBlur={blurBorder}
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
              <p style={{ fontFamily: MONO, fontSize: '0.63rem', color: 'var(--fg-muted)' }}>
                {t('newProject.privateRepo.newPatSecretHint', { name: projectName || 'project' })}
              </p>
            </div>
          )}

          {/* New SSH Key */}
          {cred.mode === 'new_ssh' && (
            <div className="flex flex-col gap-3">
              <p style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)', lineHeight: 1.6 }}
                dangerouslySetInnerHTML={{ __html: t('newProject.privateRepo.privateKeyHint') }}
              />
              <div>
                <label style={labelStyle}>
                  {t('newProject.privateRepo.privateKey')}{' '}
                  <span style={{ color: 'var(--danger)', fontWeight: 400 }}>{t('newProject.privateRepo.writeOnly')}</span>
                </label>
                <textarea
                  value={cred.sshKeyValue}
                  onChange={e => onChange({ ...cred, sshKeyValue: e.target.value })}
                  placeholder={'-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----'}
                  rows={6}
                  style={{ ...inputStyle, resize: 'vertical', fontSize: '0.75rem' }}
                  onFocus={focusAccent}
                  onBlur={blurBorder}
                />
              </div>
              <p style={{ fontFamily: MONO, fontSize: '0.63rem', color: 'var(--fg-muted)' }}>
                {t('newProject.privateRepo.newSshSecretHint', { name: projectName || 'project' })}
              </p>
            </div>
          )}

          {/* Existing secret */}
          {cred.mode === 'existing' && (
            <div className="flex flex-col gap-3">
              <div>
                <label style={labelStyle}>{t('newProject.privateRepo.selectSecret')}</label>
                {secrets.length === 0 ? (
                  <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)' }}>
                    {t('newProject.privateRepo.noSecrets')}{' '}
                    <a href="/secrets" style={{ color: 'var(--accent)' }}>{t('newProject.privateRepo.createOne')}</a>{' '}
                    {t('newProject.privateRepo.noSecretsFirst')}
                  </p>
                ) : (
                  <select
                    value={cred.existingSecretId}
                    onChange={e => onChange({ ...cred, existingSecretId: e.target.value })}
                    style={{ ...inputStyle, cursor: 'pointer' }}
                    onFocus={focusAccent}
                    onBlur={blurBorder}
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
                    <label style={labelStyle}>{t('newProject.privateRepo.gitUsername')}</label>
                    <input
                      value={cred.patGitUsername || 'x-access-token'}
                      onChange={e => onChange({ ...cred, patGitUsername: e.target.value })}
                      placeholder="x-access-token"
                      style={inputStyle}
                      onFocus={focusAccent}
                      onBlur={blurBorder}
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
  const [form, setForm] = useState<Partial<Project>>({
    git_branch: 'main',
    dockerfile_path: 'Dockerfile',
    auth_required: false,
    auth_allowed_domains: '',
    memory_limit: '4g',
  })
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

    // Validate credential inputs
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

    setSaving(true)
    try {
      // Step 1: create the project
      const project = await api.projects.create(form)

      // Step 2: create + bind credential (if any)
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
    <label style={labelStyle}>
      {text.toUpperCase()}
      {required && <span style={{ color: 'var(--danger)', marginLeft: '0.3em' }}>*</span>}
    </label>
  )

  return (
    <div className="page-enter" style={{ maxWidth: '520px' }}>
        <div className="mb-8">
          <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.15em' }}>{t('newProject.sectionLabel')}</p>
          <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>{t('newProject.heading')}</h1>
        </div>

        <form onSubmit={handleSubmit} className="flex flex-col gap-5">
          {/* Name */}
          <div>
            {fieldLabel(t('newProject.fields.name'))}
            <input
              value={form.name ?? ''}
              onChange={e => setForm({ ...form, name: e.target.value })}
              required
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
          </div>

          {/* Git URL */}
          <div>
            {fieldLabel(t('newProject.fields.gitUrl'))}
            <input
              value={form.git_url ?? ''}
              onChange={e => setForm({ ...form, git_url: e.target.value })}
              required
              placeholder="https://github.com/owner/repo.git"
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
          </div>

          {/* Private repo credential shortcut — appears when git URL is filled */}
          {(form.git_url ?? '').trim() !== '' && (
            <PrivateRepoSection
              gitUrl={form.git_url ?? ''}
              projectName={form.name ?? ''}
              cred={cred}
              onChange={setCred}
            />
          )}

          {/* Branch */}
          <div>
            {fieldLabel(t('newProject.fields.gitBranch'), false)}
            <input
              value={form.git_branch ?? ''}
              onChange={e => setForm({ ...form, git_branch: e.target.value })}
              placeholder="main"
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
          </div>

          {/* Domain prefix */}
          <div>
            <label style={labelStyle}>
              {t('newProject.fields.domainPrefix').toUpperCase()}
              {domainPrefixRequired && <span style={{ color: 'var(--danger)', marginLeft: '0.3em' }}>*</span>}
            </label>
            <input
              value={form.domain_prefix ?? ''}
              onChange={e => setForm({ ...form, domain_prefix: e.target.value })}
              required={domainPrefixRequired}
              placeholder={nameIsValidPrefix ? form.name : undefined}
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
            <p style={{ fontFamily: MONO, fontSize: '0.63rem', marginTop: '0.35rem', color: domainPrefixRequired ? 'var(--danger)' : 'var(--fg-muted)' }}>
              {nameIsValidPrefix
                ? t('newProject.domainOptional', { name: form.name })
                : t('newProject.domainRequired')}
            </p>
          </div>

          {/* Dockerfile path */}
          <div>
            {fieldLabel(t('newProject.fields.dockerfilePath'), false)}
            <input
              value={form.dockerfile_path ?? ''}
              onChange={e => setForm({ ...form, dockerfile_path: e.target.value })}
              placeholder="Dockerfile"
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
          </div>

          {/* Memory limit */}
          <div>
            {fieldLabel(t('newProject.fields.memoryLimit'), false)}
            <input
              value={form.memory_limit ?? ''}
              onChange={e => setForm({ ...form, memory_limit: e.target.value })}
              placeholder="4g"
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
            <p style={{ fontFamily: MONO, fontSize: '0.63rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('newProject.fields.memoryLimitHint')}
            </p>
          </div>

          {/* Persistent storage path */}
          <div>
            {fieldLabel(t('newProject.fields.volumeMountPath'), false)}
            <input
              value={form.volume_mount_path ?? ''}
              onChange={e => setForm({ ...form, volume_mount_path: e.target.value })}
              placeholder="/workspace"
              style={inputStyle}
              onFocus={focusAccent}
              onBlur={blurBorder}
            />
            <p style={{ fontFamily: MONO, fontSize: '0.63rem', marginTop: '0.35rem', color: 'var(--fg-muted)' }}>
              {t('newProject.fields.volumeMountPathHint')}
            </p>
          </div>

          {error && (
            <p style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--danger)' }}>{error}</p>
          )}

          <button
            type="submit"
            disabled={saving}
            style={{
              background: 'var(--accent)',
              color: '#0f0f0f',
              fontFamily: MONO,
              fontSize: '0.85rem',
              fontWeight: 500,
              padding: '0.6rem 1.5rem',
              border: 'none',
              borderRadius: '2px',
              cursor: saving ? 'not-allowed' : 'pointer',
              opacity: saving ? 0.7 : 1,
              alignSelf: 'flex-start',
            }}
          >
            {saving ? t('newProject.creating') : t('newProject.createProject')}
          </button>
        </form>
    </div>
  )
}
