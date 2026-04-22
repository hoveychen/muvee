import { useEffect, useState } from 'react'
import { Plus, Copy, Check, Key, Trash2, Eye, EyeOff } from 'lucide-react'
import { api } from '../lib/api'
import type { ApiToken } from '../lib/types'
import { timeAgo } from '../lib/utils'
import { useTranslation } from 'react-i18next'

const MONO = 'var(--font-mono)'

type ExpiryChoice = 'never' | '30d' | '90d' | '365d'

const EXPIRY_HOURS: Record<ExpiryChoice, number> = {
  never: 0,
  '30d': 30 * 24,
  '90d': 90 * 24,
  '365d': 365 * 24,
}

function expiryToDuration(choice: ExpiryChoice): string | undefined {
  const h = EXPIRY_HOURS[choice]
  return h > 0 ? `${h}h` : undefined
}

function formatExpiry(iso: string | null | undefined, t: (k: string) => string): string {
  if (!iso) return t('settingsTokens.never')
  const d = new Date(iso)
  if (d.getTime() <= Date.now()) return t('settingsTokens.expired')
  return d.toLocaleDateString()
}

export default function SettingsTokensPage() {
  const { t } = useTranslation()
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [loading, setLoading] = useState(true)
  const [name, setName] = useState('')
  const [expiry, setExpiry] = useState<ExpiryChoice>('90d')
  const [creating, setCreating] = useState(false)
  const [createdToken, setCreatedToken] = useState<string | null>(null)
  const [createdExpiresAt, setCreatedExpiresAt] = useState<string | undefined>(undefined)
  const [revealed, setRevealed] = useState(false)
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = () => {
    api.meTokens.list()
      .then(setTokens)
      .catch(e => setError(e.message || String(e)))
      .finally(() => setLoading(false))
  }
  useEffect(load, [])

  const handleCreate = async () => {
    if (creating) return
    setCreating(true)
    setError(null)
    try {
      const res = await api.meTokens.create({
        name: name.trim() || t('settingsTokens.defaultName'),
        expires_in: expiryToDuration(expiry),
      })
      setCreatedToken(res.token)
      setCreatedExpiresAt(res.expires_at)
      setName('')
      setRevealed(false)
      load()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm(t('settingsTokens.deleteConfirm'))) return
    await api.meTokens.delete(id)
    load()
  }

  const copy = (val: string) => {
    navigator.clipboard.writeText(val)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div style={{ padding: '1.5rem 2rem', maxWidth: '960px' }}>
      <h1 style={{ fontSize: '1.25rem', fontWeight: 600, marginBottom: '0.5rem' }}>
        {t('settingsTokens.title')}
      </h1>
      <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', lineHeight: 1.6, marginBottom: '1.5rem' }}>
        {t('settingsTokens.hint')}
      </p>

      {/* Create form */}
      <div className="card" style={{ padding: '1rem', marginBottom: '1rem' }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr auto auto', gap: '0.5rem', alignItems: 'end' }}>
          <div>
            <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', display: 'block', marginBottom: '0.25rem' }}>
              {t('settingsTokens.nameLabel')}
            </label>
            <input
              type="text"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={t('settingsTokens.namePlaceholder')}
              className="form-input"
              style={{ fontSize: '0.875rem', width: '100%' }}
              onKeyDown={e => e.key === 'Enter' && handleCreate()}
            />
          </div>
          <div>
            <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', display: 'block', marginBottom: '0.25rem' }}>
              {t('settingsTokens.expiryLabel')}
            </label>
            <select
              value={expiry}
              onChange={e => setExpiry(e.target.value as ExpiryChoice)}
              className="form-input"
              style={{ fontSize: '0.875rem' }}
            >
              <option value="30d">{t('settingsTokens.expiry.30d')}</option>
              <option value="90d">{t('settingsTokens.expiry.90d')}</option>
              <option value="365d">{t('settingsTokens.expiry.365d')}</option>
              <option value="never">{t('settingsTokens.expiry.never')}</option>
            </select>
          </div>
          <button
            onClick={handleCreate}
            disabled={creating}
            className="btn-primary"
            style={{ display: 'flex', alignItems: 'center', gap: '6px', ...(creating ? { opacity: 0.6, cursor: 'not-allowed' } : {}) }}
          >
            <Plus size={13} />
            {creating ? t('settingsTokens.creating') : t('settingsTokens.create')}
          </button>
        </div>
        {error && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginTop: '0.5rem' }}>{error}</p>
        )}
      </div>

      {/* Newly created token (shown once) */}
      {createdToken && (
        <div className="card" style={{
          background: 'rgba(37,99,235,0.08)',
          borderColor: 'rgba(37,99,235,0.3)',
          padding: '0.75rem 1rem',
          marginBottom: '1rem',
        }}>
          <p style={{ fontSize: '0.8125rem', color: 'var(--accent)', marginBottom: '0.4rem', fontWeight: 600 }}>
            {t('settingsTokens.created')}
          </p>
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <code style={{
              fontFamily: MONO, fontSize: '0.875rem', color: 'var(--fg-primary)', flex: 1, wordBreak: 'break-all',
            }}>
              {revealed ? createdToken : '•'.repeat(40)}
            </code>
            <button
              onClick={() => setRevealed(v => !v)}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--fg-muted)', padding: '4px' }}
            >
              {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
            <button
              onClick={() => copy(createdToken)}
              className="btn-secondary"
              style={{
                display: 'flex', alignItems: 'center', gap: '4px',
                fontSize: '0.8125rem', padding: '3px 8px',
                color: copied ? 'var(--success)' : 'var(--fg-muted)',
              }}
            >
              {copied ? <Check size={12} /> : <Copy size={12} />}
              {copied ? t('settingsTokens.copied') : t('settingsTokens.copy')}
            </button>
          </div>
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '0.4rem' }}>
            {t('settingsTokens.copyWarning')}
            {createdExpiresAt && ` ${t('settingsTokens.expiresOn')} ${new Date(createdExpiresAt).toLocaleString()}.`}
          </p>
        </div>
      )}

      {/* Token list */}
      {loading ? (
        <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>{t('common.loading')}</p>
      ) : tokens.length === 0 ? (
        <div className="card" style={{ padding: '2rem', textAlign: 'center' }}>
          <Key size={28} style={{ color: 'var(--fg-muted)', margin: '0 auto 0.5rem' }} />
          <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
            {t('settingsTokens.empty')}
          </p>
        </div>
      ) : (
        <div className="table-container">
          <table className="w-full border-collapse">
            <thead>
              <tr>
                {[
                  t('settingsTokens.columns.name'),
                  t('settingsTokens.columns.expires'),
                  t('settingsTokens.columns.lastUsed'),
                  t('settingsTokens.columns.created'),
                  '',
                ].map(h => (
                  <th key={h} style={{
                    fontSize: '0.8125rem', color: 'var(--fg-muted)', letterSpacing: '0.04em',
                    padding: '0.6rem 1rem', textAlign: 'left', fontWeight: 500,
                  }}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {tokens.map(tok => (
                <tr key={tok.id}>
                  <td style={{ padding: '0.6rem 1rem', fontSize: '0.875rem' }}>{tok.name}</td>
                  <td style={{ padding: '0.6rem 1rem', fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                    {formatExpiry(tok.expires_at, t)}
                  </td>
                  <td style={{ padding: '0.6rem 1rem', fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                    {tok.last_used_at ? timeAgo(tok.last_used_at) : t('settingsTokens.neverUsed')}
                  </td>
                  <td style={{ padding: '0.6rem 1rem', fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                    {timeAgo(tok.created_at)}
                  </td>
                  <td style={{ padding: '0.6rem 1rem', textAlign: 'right' }}>
                    <button
                      onClick={() => handleDelete(tok.id)}
                      className="btn-secondary"
                      style={{ color: 'var(--danger)', padding: '3px 8px', fontSize: '0.8125rem' }}
                      title={t('settingsTokens.delete')}
                    >
                      <Trash2 size={13} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
