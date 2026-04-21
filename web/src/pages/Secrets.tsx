import { useEffect, useState } from 'react'
import { KeyRound, Plus, Trash2, Eye, EyeOff, Lock, AlertTriangle } from 'lucide-react'
import { api } from '../lib/api'
import type { Secret, SecretType } from '../lib/types'
import { timeAgo } from '../lib/utils'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

const SECRET_TYPES: SecretType[] = ['password', 'ssh_key', 'api_key', 'env_var']

function typeLabelKey(type: SecretType): string {
  switch (type) {
    case 'ssh_key': return 'secrets.sshKey'
    case 'api_key': return 'secrets.apiKey'
    case 'env_var': return 'secrets.envVar'
    default: return 'secrets.password'
  }
}

function typeBadgeClass(type: SecretType): string {
  switch (type) {
    case 'ssh_key': return 'badge-info'
    case 'api_key': return 'badge-warning'
    case 'env_var': return 'badge-success'
    default: return 'badge-neutral'
  }
}

function formTypeLabelKey(type: SecretType): string {
  switch (type) {
    case 'ssh_key': return 'secrets.form.sshKey'
    case 'api_key': return 'secrets.form.apiKey'
    case 'env_var': return 'secrets.form.envVar'
    default: return 'secrets.form.passwordToken'
  }
}

export default function SecretsPage() {
  const [secrets, setSecrets] = useState<Secret[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [secretsEnabled, setSecretsEnabled] = useState<boolean | null>(null)
  const { t } = useTranslation()

  useEffect(() => {
    api.runtime.config().then(cfg => setSecretsEnabled(cfg.secrets_enabled)).catch(() => {})
    api.secrets.list().then(setSecrets).finally(() => setLoading(false))
  }, [])

  const handleDelete = async (id: string) => {
    if (!confirm(t('secrets.deleteConfirm'))) return
    await api.secrets.delete(id).catch(e => alert(t('secrets.form.failed') + e.message))
    setSecrets(prev => prev.filter(s => s.id !== id))
  }

  return (
    <div className="page-enter">
      <div className="page-header flex items-end justify-between">
        <div>
          <p className="page-subtitle">
            {t('secrets.sectionLabel')}
          </p>
          <h1 className="page-title">
            {t('secrets.heading')}
          </h1>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          disabled={secretsEnabled === false}
          className="btn-primary flex items-center gap-2"
        >
          <Plus size={14} />
          {t('secrets.newSecret')}
        </button>
      </div>

      {secretsEnabled === false && (
        <div
          className="mb-6 px-4 py-3 rounded-md flex items-start gap-3"
          style={{ background: 'rgba(217, 119, 6, 0.1)', border: '1px solid rgba(217, 119, 6, 0.4)' }}
        >
          <AlertTriangle size={16} color="var(--warning)" style={{ flexShrink: 0, marginTop: '1px' }} />
          <p
            style={{ fontSize: '0.8125rem', color: 'var(--warning)', lineHeight: 1.7 }}
            dangerouslySetInnerHTML={{ __html: t('secrets.disabledBanner') }}
          />
        </div>
      )}

      {/* Info banner */}
      <div className="card mb-6 px-4 py-3">
        <p
          style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', lineHeight: 1.7 }}
          dangerouslySetInnerHTML={{ __html: t('secrets.infoText') }}
        />
      </div>

      {showCreate && (
        <CreateSecretForm
          onCreated={sec => {
            setSecrets(prev => [sec, ...prev])
            setShowCreate(false)
          }}
          onCancel={() => setShowCreate(false)}
        />
      )}

      {loading ? (
        <div style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>{t('secrets.loading')}</div>
      ) : secrets.length === 0 && !showCreate ? (
        <EmptyState onNew={() => setShowCreate(true)} />
      ) : (
        <div className="table-container">
          {/* Table header */}
          <div
            className="grid gap-4 px-5 py-3"
            style={{
              gridTemplateColumns: '1fr 120px 180px 48px',
              borderBottom: '1px solid var(--border)',
              fontSize: '0.75rem',
              fontWeight: 600,
              color: 'var(--fg-muted)',
              letterSpacing: '0.04em',
              textTransform: 'uppercase',
            }}
          >
            <span>{t('secrets.columns.name')}</span>
            <span>{t('secrets.columns.type')}</span>
            <span>{t('secrets.columns.created')}</span>
            <span></span>
          </div>

          {secrets.map((sec, i) => (
            <SecretRow key={sec.id} secret={sec} index={i} total={secrets.length} onDelete={handleDelete} />
          ))}
        </div>
      )}
    </div>
  )
}

function SecretRow({ secret, index, total, onDelete }: { secret: Secret; index: number; total: number; onDelete: (id: string) => void }) {
  const { t } = useTranslation()
  const typeLabel = t(typeLabelKey(secret.type))

  return (
    <div
      className="grid gap-4 px-5 py-4 items-center"
      style={{
        gridTemplateColumns: '1fr 120px 180px 48px',
        borderBottom: index < total - 1 ? '1px solid var(--border)' : 'none',
        transition: 'background 0.1s',
        background: 'var(--bg-card)',
      }}
      onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.background = 'var(--bg-hover)' }}
      onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.background = 'var(--bg-card)' }}
    >
      <div className="flex flex-col gap-1 min-w-0">
        <div className="flex items-center gap-3">
          <Lock size={14} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />
          <span style={{ fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)', fontWeight: 500 }}>
            {secret.name}
          </span>
        </div>
        {secret.value_preview && (
          <span
            style={{
              fontFamily: MONO,
              fontSize: '0.75rem',
              color: 'var(--fg-muted)',
              marginLeft: '1.625rem',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
            title={secret.value_preview}
          >
            {secret.value_preview}
          </span>
        )}
      </div>

      <span className={`badge ${typeBadgeClass(secret.type)}`}>
        {typeLabel}
      </span>

      <span style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
        {timeAgo(secret.created_at)}
      </span>

      <button
        onClick={() => onDelete(secret.id)}
        className="flex items-center justify-center p-1.5 rounded-md transition-all duration-150"
        style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)' }}
        onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--danger)' }}
        onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = 'var(--fg-muted)' }}
        title={t('secrets.deleteTitle')}
      >
        <Trash2 size={14} />
      </button>
    </div>
  )
}

function CreateSecretForm({ onCreated, onCancel }: { onCreated: (s: Secret) => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [type, setType] = useState<SecretType>('password')
  const [value, setValue] = useState('')
  const [showValue, setShowValue] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const { t } = useTranslation()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim() || !value.trim()) { setError(t('secrets.form.validation')); return }
    setSaving(true)
    setError('')
    try {
      const sec = await api.secrets.create({ name: name.trim(), type, value })
      onCreated(sec)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="card mb-6 p-5">
      <p style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '1.25rem' }}>
        {t('secrets.form.title')}
      </p>

      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        {/* Name */}
        <div>
          <label className="form-label">
            {t('secrets.form.name')}
          </label>
          <input
            className="form-input"
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder="e.g. GITHUB_TOKEN"
            style={{ fontFamily: MONO }}
          />
        </div>

        {/* Type */}
        <div>
          <label className="form-label">
            {t('secrets.form.type')}
          </label>
          <div className="flex gap-2 flex-wrap">
            {SECRET_TYPES.map(typOpt => (
              <button
                key={typOpt}
                type="button"
                onClick={() => setType(typOpt)}
                className={type === typOpt ? 'btn-primary' : 'btn-secondary'}
                style={{ fontSize: '0.8125rem' }}
              >
                {t(formTypeLabelKey(typOpt))}
              </button>
            ))}
          </div>
          {type === 'ssh_key' && (
            <p
              style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.5rem' }}
              dangerouslySetInnerHTML={{ __html: t('secrets.form.sshKeyHint') }}
            />
          )}
          {type === 'api_key' && (
            <p
              style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.5rem' }}
              dangerouslySetInnerHTML={{ __html: t('secrets.form.apiKeyHint') }}
            />
          )}
          {type === 'env_var' && (
            <p
              style={{ fontSize: '0.8125rem', color: 'var(--warning)', marginTop: '0.5rem' }}
              dangerouslySetInnerHTML={{ __html: t('secrets.form.envVarHint') }}
            />
          )}
        </div>

        {/* Value */}
        <div>
          <label className="form-label">
            {t('secrets.form.value')} <span style={{ color: 'var(--danger)', fontWeight: 400 }}>{t('secrets.form.valueWriteOnly')}</span>
          </label>
          <div className="relative">
            {type === 'ssh_key' ? (
              <textarea
                className="form-input"
                value={value}
                onChange={e => setValue(e.target.value)}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;..."
                rows={6}
                style={{ fontFamily: MONO, resize: 'vertical' }}
              />
            ) : (
              <div className="relative flex items-center">
                <input
                  type={showValue ? 'text' : 'password'}
                  className="form-input"
                  value={value}
                  onChange={e => setValue(e.target.value)}
                  placeholder={t('secrets.form.secretPlaceholder')}
                  style={{ paddingRight: '2.5rem' }}
                />
                <button
                  type="button"
                  onClick={() => setShowValue(v => !v)}
                  style={{ position: 'absolute', right: '0.5rem', background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)' }}
                >
                  {showValue ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              </div>
            )}
          </div>
        </div>

        {error && (
          <p style={{ fontSize: '0.875rem', color: 'var(--danger)' }}>{error}</p>
        )}

        <div className="flex gap-2">
          <button
            type="submit"
            disabled={saving}
            className="btn-primary"
          >
            {saving ? t('secrets.form.saving') : t('secrets.form.save')}
          </button>
          <button
            type="button"
            onClick={onCancel}
            className="btn-secondary"
          >
            {t('secrets.form.cancel')}
          </button>
        </div>
      </form>
    </div>
  )
}

function EmptyState({ onNew }: { onNew: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="card flex flex-col items-center justify-center py-20">
      <KeyRound size={32} style={{ color: 'var(--fg-muted)', marginBottom: '1rem' }} />
      <p style={{ fontSize: '1.1rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '0.5rem' }}>
        {t('secrets.empty.title')}
      </p>
      <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', marginBottom: '1.5rem' }}>
        {t('secrets.empty.hint')}
      </p>
      <button
        onClick={onNew}
        className="btn-primary flex items-center gap-2"
      >
        <Plus size={14} />
        {t('secrets.newSecret')}
      </button>
    </div>
  )
}
