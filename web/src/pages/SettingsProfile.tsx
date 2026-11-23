import { useEffect, useState } from 'react'
import { Save, RotateCcw, Globe, LogOut, ExternalLink } from 'lucide-react'
import { api } from '../lib/api'
import type { VisitedProject } from '../lib/types'
import { useAuth } from '../lib/auth'
import { useTranslation } from 'react-i18next'

export default function SettingsProfilePage() {
  const { t } = useTranslation()
  const { user, refetch } = useAuth()
  const [name, setName] = useState('')
  const [avatarURL, setAvatarURL] = useState('')
  const [saving, setSaving] = useState(false)
  const [savedAt, setSavedAt] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (user) {
      setName(user.name || '')
      setAvatarURL(user.avatar_url || '')
    }
  }, [user])

  const dirty =
    !!user && (name.trim() !== (user.name || '') || avatarURL.trim() !== (user.avatar_url || ''))

  const handleSave = async () => {
    if (saving || !dirty) return
    setSaving(true)
    setError(null)
    const payload: { name?: string; avatar_url?: string } = {}
    if (user && name.trim() !== (user.name || '')) payload.name = name.trim()
    if (user && avatarURL.trim() !== (user.avatar_url || '')) payload.avatar_url = avatarURL.trim()
    try {
      await api.updateMe(payload)
      refetch()
      setSavedAt(Date.now())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const handleReset = () => {
    if (!user) return
    setName(user.name || '')
    setAvatarURL(user.avatar_url || '')
    setError(null)
  }

  if (!user) return null

  const initial = (user.name || user.email || '?').charAt(0).toUpperCase()

  return (
    <div style={{ maxWidth: '720px' }}>
      <h1 style={{ fontSize: '1.25rem', fontWeight: 600, marginBottom: '0.5rem' }}>
        {t('settingsProfile.title')}
      </h1>
      <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', lineHeight: 1.6, marginBottom: '1.5rem' }}>
        {t('settingsProfile.hint')}
      </p>

      <div className="card" style={{ padding: '1.25rem' }}>
        {/* Avatar preview row */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '1rem', marginBottom: '1.25rem' }}>
          {avatarURL ? (
            <img
              src={avatarURL}
              alt=""
              className="rounded-full"
              style={{ width: '56px', height: '56px', objectFit: 'cover', background: 'var(--bg-hover)' }}
              onError={e => { (e.currentTarget as HTMLImageElement).style.visibility = 'hidden' }}
              onLoad={e => { (e.currentTarget as HTMLImageElement).style.visibility = 'visible' }}
            />
          ) : (
            <div className="rounded-full flex items-center justify-center" style={{ width: '56px', height: '56px', background: 'var(--bg-hover)', color: 'var(--fg-primary)', fontSize: '1.25rem', fontWeight: 600 }}>
              {initial}
            </div>
          )}
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: '0.9375rem', fontWeight: 600, color: 'var(--fg-primary)' }}>
              {name || user.email.split('@')[0]}
            </div>
            <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{user.email}</div>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
          <div>
            <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', display: 'block', marginBottom: '0.25rem' }}>
              {t('settingsProfile.nameLabel')}
            </label>
            <input
              type="text"
              value={name}
              onChange={e => setName(e.target.value)}
              maxLength={100}
              placeholder={t('settingsProfile.namePlaceholder')}
              className="form-input"
              style={{ fontSize: '0.875rem', width: '100%' }}
            />
            {user.name_overridden && (
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.25rem' }}>
                {t('settingsProfile.nameOverriddenHint')}
              </p>
            )}
          </div>

          <div>
            <label style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', display: 'block', marginBottom: '0.25rem' }}>
              {t('settingsProfile.avatarLabel')}
            </label>
            <input
              type="url"
              value={avatarURL}
              onChange={e => setAvatarURL(e.target.value)}
              placeholder="https://example.com/avatar.png"
              className="form-input"
              style={{ fontSize: '0.875rem', width: '100%' }}
            />
            {user.avatar_overridden && (
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '0.25rem' }}>
                {t('settingsProfile.avatarOverriddenHint')}
              </p>
            )}
          </div>
        </div>

        {error && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginTop: '0.75rem' }}>{error}</p>
        )}
        {savedAt && !error && !dirty && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--success)', marginTop: '0.75rem' }}>
            {t('settingsProfile.saved')}
          </p>
        )}

        <div style={{ display: 'flex', gap: '0.5rem', marginTop: '1rem', justifyContent: 'flex-end' }}>
          <button
            onClick={handleReset}
            disabled={!dirty || saving}
            className="btn-secondary"
            style={{ display: 'flex', alignItems: 'center', gap: '6px', ...(!dirty || saving ? { opacity: 0.6, cursor: 'not-allowed' } : {}) }}
          >
            <RotateCcw size={13} />
            {t('settingsProfile.reset')}
          </button>
          <button
            onClick={handleSave}
            disabled={!dirty || saving}
            className="btn-primary"
            style={{ display: 'flex', alignItems: 'center', gap: '6px', ...(!dirty || saving ? { opacity: 0.6, cursor: 'not-allowed' } : {}) }}
          >
            <Save size={13} />
            {saving ? t('settingsProfile.saving') : t('settingsProfile.save')}
          </button>
        </div>
      </div>

      <SubsiteSignInsCard />
    </div>
  )
}

// "Subsite sign-ins" (BACKLOG-20261121-001 X2): recently visited subsites,
// each linking to its /_oauth/me account page, and "sign out of all subsites".
function SubsiteSignInsCard() {
  const { t } = useTranslation()
  const [visited, setVisited] = useState<VisitedProject[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [revoking, setRevoking] = useState(false)
  const [revoked, setRevoked] = useState(false)
  const [revokeError, setRevokeError] = useState<string | null>(null)

  useEffect(() => {
    api.visitedProjects.list()
      .then(rows => setVisited(rows ?? []))
      .catch(e => setLoadError((e as Error).message))
  }, [])

  const handleRevoke = async () => {
    if (revoking || !confirm(t('settingsProfile.subsites.revokeConfirm'))) return
    setRevoking(true)
    setRevokeError(null)
    setRevoked(false)
    try {
      await api.meFwdSessions.revokeAll()
      setRevoked(true)
    } catch (e) {
      setRevokeError((e as Error).message)
    } finally {
      setRevoking(false)
    }
  }

  return (
    <>
      <h2 style={{ fontSize: '1rem', fontWeight: 600, marginTop: '2rem', marginBottom: '0.5rem', display: 'flex', alignItems: 'center', gap: '6px' }}>
        <Globe size={15} />
        {t('settingsProfile.subsites.title')}
      </h2>
      <div className="card" style={{ padding: '1rem' }}>
        <p style={{ fontSize: '0.875rem', color: 'var(--fg-muted)', lineHeight: 1.6, marginBottom: '0.75rem' }}>
          {t('settingsProfile.subsites.hint')}
        </p>

        <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginBottom: '0.375rem' }}>
          {t('settingsProfile.subsites.recentLabel')}
        </div>
        {loadError && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginBottom: '0.75rem' }}>{loadError}</p>
        )}
        {!loadError && visited === null && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem' }}>{t('settingsProfile.subsites.loading')}</p>
        )}
        {visited && visited.length === 0 && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginBottom: '0.75rem' }}>{t('settingsProfile.subsites.empty')}</p>
        )}
        {visited && visited.length > 0 && (
          <ul style={{ listStyle: 'none', padding: 0, margin: '0 0 0.75rem 0', display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
            {visited.map(v => (
              <li key={v.project_id} style={{ display: 'flex', alignItems: 'baseline', gap: '0.5rem', fontSize: '0.8125rem', flexWrap: 'wrap' }}>
                <span style={{ fontWeight: 600, color: 'var(--fg-primary)' }}>{v.name}</span>
                <a href={v.account_url} target="_blank" rel="noopener noreferrer" style={{ color: 'var(--accent)', display: 'inline-flex', alignItems: 'center', gap: '3px' }} title={t('settingsProfile.subsites.openAccount')}>
                  {v.host}
                  <ExternalLink size={11} />
                </a>
                <span style={{ color: 'var(--fg-muted)', marginLeft: 'auto' }}>
                  {t('settingsProfile.subsites.lastSeen', { time: new Date(v.last_seen_at).toLocaleString() })}
                </span>
              </li>
            ))}
          </ul>
        )}

        <button
          onClick={handleRevoke}
          disabled={revoking}
          className="btn-secondary"
          style={{ color: 'var(--danger)', fontSize: '0.875rem', display: 'flex', alignItems: 'center', gap: '6px', ...(revoking ? { opacity: 0.6, cursor: 'not-allowed' } : {}) }}
        >
          <LogOut size={13} />
          {revoking ? t('settingsProfile.subsites.revoking') : t('settingsProfile.subsites.revokeAll')}
        </button>
        {revoked && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--success)', marginTop: '0.5rem' }}>{t('settingsProfile.subsites.revoked')}</p>
        )}
        {revokeError && (
          <p style={{ fontSize: '0.8125rem', color: 'var(--danger)', marginTop: '0.5rem' }}>{revokeError}</p>
        )}
      </div>
    </>
  )
}
