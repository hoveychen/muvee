import { useEffect, useState } from 'react'
import { KeyRound, Plus, Trash2, Eye, EyeOff, Lock } from 'lucide-react'
import { api } from '../lib/api'
import type { Secret } from '../lib/types'
import { timeAgo } from '../lib/utils'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

type SecretType = 'password' | 'ssh_key'

export default function SecretsPage() {
  const [secrets, setSecrets] = useState<Secret[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const { t } = useTranslation()

  useEffect(() => {
    api.secrets.list().then(setSecrets).finally(() => setLoading(false))
  }, [])

  const handleDelete = async (id: string) => {
    if (!confirm(t('secrets.deleteConfirm'))) return
    await api.secrets.delete(id).catch(e => alert(t('secrets.form.failed') + e.message))
    setSecrets(prev => prev.filter(s => s.id !== id))
  }

  return (
    <div className="page-enter">
      <div className="flex items-end justify-between mb-8">
        <div>
          <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.72rem', letterSpacing: '0.05em' }}>
            {t('secrets.sectionLabel')}
          </p>
          <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>
            {t('secrets.heading')}
          </h1>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-3 py-1.5 rounded-md text-sm transition-all duration-150"
          style={{
            background: 'var(--accent)',
            color: '#ffffff',
            fontWeight: 500,
            border: 'none',
            cursor: 'pointer',
          }}
        >
          <Plus size={14} />
          {t('secrets.newSecret')}
        </button>
      </div>

      {/* Info banner */}
      <div
        className="mb-6 px-4 py-3 rounded-md"
        style={{ background: 'var(--bg-card)', border: '1px solid var(--border)' }}
      >
        <p
          style={{ fontFamily: MONO, fontSize: '0.75rem', color: 'var(--fg-muted)', lineHeight: 1.7 }}
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
        <div style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.8rem' }}>{t('secrets.loading')}</div>
      ) : secrets.length === 0 && !showCreate ? (
        <EmptyState onNew={() => setShowCreate(true)} />
      ) : (
        <div style={{ border: '1px solid var(--border)', borderRadius: '6px', background: 'var(--bg-card)', overflow: 'hidden' }}>
          {/* Table header */}
          <div
            className="grid gap-4 px-5 py-3"
            style={{
              gridTemplateColumns: '1fr 120px 180px 48px',
              borderBottom: '1px solid var(--border)',
              fontFamily: MONO,
              fontSize: '0.65rem',
              color: 'var(--fg-muted)',
              letterSpacing: '0.08em',
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
  const typeLabel = secret.type === 'ssh_key' ? t('secrets.sshKey') : t('secrets.password')

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
      <div className="flex items-center gap-3">
        <Lock size={14} style={{ color: 'var(--fg-muted)', flexShrink: 0 }} />
        <span style={{ fontFamily: MONO, fontSize: '0.88rem', color: 'var(--fg-primary)', fontWeight: 500 }}>
          {secret.name}
        </span>
      </div>

      <span
        style={{
          fontFamily: MONO,
          fontSize: '0.68rem',
          padding: '2px 8px',
          borderRadius: '2em',
          background: secret.type === 'ssh_key' ? 'rgba(88,166,255,0.15)' : 'rgba(188,140,255,0.15)',
          color: secret.type === 'ssh_key' ? 'var(--accent)' : '#bc8cff',
          letterSpacing: '0.03em',
          display: 'inline-block',
        }}
      >
        {typeLabel}
      </span>

      <span style={{ fontFamily: MONO, fontSize: '0.78rem', color: 'var(--fg-muted)' }}>
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

  const inputBase = {
    width: '100%',
    background: 'var(--bg-hover)',
    border: '1px solid var(--border)',
    color: 'var(--fg-primary)',
    fontFamily: MONO,
    borderRadius: '6px',
    outline: 'none',
  }

  return (
    <div
      className="mb-6 p-5 rounded-md"
      style={{ border: '1px solid var(--border)', background: 'var(--bg-card)' }}
    >
      <p style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '1.25rem' }}>
        {t('secrets.form.title')}
      </p>

      <form onSubmit={handleSubmit} className="flex flex-col gap-4">
        {/* Name */}
        <div>
          <label style={{ fontFamily: MONO, fontSize: '0.68rem', color: 'var(--fg-muted)', letterSpacing: '0.08em', display: 'block', marginBottom: '0.4rem' }}>
            {t('secrets.form.name')}
          </label>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder="e.g. GITHUB_TOKEN"
            style={{ ...inputBase, fontSize: '0.875rem', padding: '0.5rem 0.75rem' }}
          />
        </div>

        {/* Type */}
        <div>
          <label style={{ fontFamily: MONO, fontSize: '0.68rem', color: 'var(--fg-muted)', letterSpacing: '0.08em', display: 'block', marginBottom: '0.4rem' }}>
            {t('secrets.form.type')}
          </label>
          <div className="flex gap-2">
            {(['password', 'ssh_key'] as SecretType[]).map(typOpt => (
              <button
                key={typOpt}
                type="button"
                onClick={() => setType(typOpt)}
                style={{
                  fontFamily: MONO,
                  fontSize: '0.78rem',
                  padding: '0.4rem 1rem',
                  borderRadius: '6px',
                  border: `1px solid ${type === typOpt ? 'var(--accent)' : 'var(--border)'}`,
                  background: type === typOpt ? 'rgba(88,166,255,0.1)' : 'var(--bg-hover)',
                  color: type === typOpt ? 'var(--accent)' : 'var(--fg-muted)',
                  cursor: 'pointer',
                }}
              >
                {typOpt === 'ssh_key' ? t('secrets.form.sshKey') : t('secrets.form.passwordToken')}
              </button>
            ))}
          </div>
          {type === 'ssh_key' && (
            <p
              style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginTop: '0.5rem' }}
              dangerouslySetInnerHTML={{ __html: t('secrets.form.sshKeyHint') }}
            />
          )}
        </div>

        {/* Value */}
        <div>
          <label style={{ fontFamily: MONO, fontSize: '0.68rem', color: 'var(--fg-muted)', letterSpacing: '0.08em', display: 'block', marginBottom: '0.4rem' }}>
            {t('secrets.form.value')} <span style={{ color: 'var(--danger)', fontWeight: 400 }}>{t('secrets.form.valueWriteOnly')}</span>
          </label>
          <div className="relative">
            {type === 'ssh_key' ? (
              <textarea
                value={value}
                onChange={e => setValue(e.target.value)}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;..."
                rows={6}
                style={{ ...inputBase, fontSize: '0.82rem', padding: '0.5rem 0.75rem', resize: 'vertical' }}
              />
            ) : (
              <div className="relative flex items-center">
                <input
                  type={showValue ? 'text' : 'password'}
                  value={value}
                  onChange={e => setValue(e.target.value)}
                  placeholder={t('secrets.form.secretPlaceholder')}
                  style={{ ...inputBase, fontSize: '0.875rem', padding: '0.5rem 2.5rem 0.5rem 0.75rem' }}
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
          <p style={{ fontFamily: MONO, fontSize: '0.78rem', color: 'var(--danger)' }}>{error}</p>
        )}

        <div className="flex gap-2">
          <button
            type="submit"
            disabled={saving}
            style={{
              fontFamily: MONO,
              fontSize: '0.82rem',
              padding: '0.5rem 1.25rem',
              borderRadius: '6px',
              background: 'var(--accent)',
              color: '#ffffff',
              border: 'none',
              cursor: saving ? 'not-allowed' : 'pointer',
              opacity: saving ? 0.7 : 1,
              fontWeight: 500,
            }}
          >
            {saving ? t('secrets.form.saving') : t('secrets.form.save')}
          </button>
          <button
            type="button"
            onClick={onCancel}
            style={{
              fontFamily: MONO,
              fontSize: '0.82rem',
              padding: '0.5rem 1.25rem',
              borderRadius: '6px',
              background: 'var(--bg-hover)',
              color: 'var(--fg-muted)',
              border: '1px solid var(--border)',
              cursor: 'pointer',
            }}
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
    <div
      className="flex flex-col items-center justify-center py-20 rounded-md"
      style={{ border: '1px solid var(--border)', background: 'var(--bg-card)' }}
    >
      <KeyRound size={32} style={{ color: 'var(--fg-muted)', marginBottom: '1rem' }} />
      <p style={{ fontSize: '1.1rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '0.5rem' }}>
        {t('secrets.empty.title')}
      </p>
      <p style={{ fontFamily: MONO, fontSize: '0.8rem', color: 'var(--fg-muted)', marginBottom: '1.5rem' }}>
        {t('secrets.empty.hint')}
      </p>
      <button
        onClick={onNew}
        className="flex items-center gap-2 px-4 py-2 rounded-md"
        style={{ background: 'var(--accent)', color: '#ffffff', fontFamily: MONO, fontSize: '0.82rem', border: 'none', cursor: 'pointer', fontWeight: 500 }}
      >
        <Plus size={14} />
        {t('secrets.newSecret')}
      </button>
    </div>
  )
}
