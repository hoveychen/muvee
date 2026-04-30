import { useEffect, useState } from 'react'
import { Save, RotateCcw } from 'lucide-react'
import { api } from '../lib/api'
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
    <div style={{ padding: '1.5rem 2rem', maxWidth: '720px' }}>
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
    </div>
  )
}
