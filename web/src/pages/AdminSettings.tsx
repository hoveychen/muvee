import { useState, useEffect, useCallback, useRef } from 'react'
import { CheckCircle, XCircle, AlertCircle, RefreshCw, Loader, Save, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useSettings } from '../lib/settings'
import type { SystemSettings, HealthReport, CertReport, CertStatus, AccessMode, RuntimeConfig } from '../lib/types'

const MONO = 'var(--font-mono)'

// ─── Field helper ─────────────────────────────────────────────────────────────

function Field({
  label, value, onChange, hint, placeholder,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  hint?: string
  placeholder?: string
}) {
  return (
    <div style={{ marginBottom: '18px' }}>
      <label className="form-label">
        {label}
      </label>
      <input
        className="form-input"
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
      />
      {hint && <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '4px' }}>{hint}</p>}
    </div>
  )
}

// ─── Image field with upload ──────────────────────────────────────────────────

function ImageField({
  label, value, onChange, hint, placeholder, uploadType, t,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  hint?: string
  placeholder?: string
  uploadType: 'logo' | 'favicon'
  t: (key: string) => string
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)

  const handleUpload = async (file: File) => {
    setUploading(true)
    try {
      const result = await api.admin.uploadBranding(uploadType, file)
      onChange(result.url)
    } catch {
      // ignore
    } finally {
      setUploading(false)
    }
  }

  return (
    <div style={{ marginBottom: '18px' }}>
      <label className="form-label">
        {label}
      </label>
      <div style={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
        <input
          className="form-input"
          value={value}
          onChange={e => onChange(e.target.value)}
          placeholder={placeholder}
          style={{ flex: 1 }}
        />
        <button
          className="btn-secondary"
          onClick={() => inputRef.current?.click()}
          disabled={uploading}
          title={t('adminSettings.branding.upload')}
          style={{ flexShrink: 0, cursor: uploading ? 'default' : 'pointer' }}
        >
          {uploading ? <Loader size={12} style={{ animation: 'spin 1s linear infinite' }} /> : <Upload size={12} />}
          {t('adminSettings.branding.upload')}
        </button>
        <input
          ref={inputRef}
          type="file"
          accept="image/*"
          style={{ display: 'none' }}
          onChange={e => {
            const file = e.target.files?.[0]
            if (file) handleUpload(file)
            e.target.value = ''
          }}
        />
      </div>
      {hint && <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '4px' }}>{hint}</p>}
      {value && (
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '8px' }}>
          <img
            src={value} alt=""
            style={{ width: '36px', height: '36px', borderRadius: '6px', objectFit: 'contain', border: '1px solid var(--border)' }}
          />
          <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{t('adminSettings.branding.preview')}</span>
        </div>
      )}
    </div>
  )
}

// ─── Health check row ─────────────────────────────────────────────────────────

function HealthRow({ check }: { check: import('../lib/types').HealthCheck }) {
  const badgeClass = check.status === 'ok'
    ? 'badge badge-success'
    : check.status === 'warning'
    ? 'badge badge-warning'
    : 'badge badge-danger'

  const icon = check.status === 'ok'
    ? <CheckCircle size={14} color="var(--success)" />
    : check.status === 'warning'
    ? <AlertCircle size={14} color="var(--warning)" />
    : <XCircle size={14} color="var(--danger)" />

  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: '10px', padding: '10px 12px', borderRadius: '6px', background: 'var(--bg-base)' }}>
      <div style={{ marginTop: '1px', flexShrink: 0 }}>{icon}</div>
      <div style={{ flex: 1 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{check.name}</span>
          <span className={badgeClass}>{check.status}</span>
        </div>
        <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>{check.message}</div>
      </div>
    </div>
  )
}

// ─── Certificate row ──────────────────────────────────────────────────────────

function CertRow({ cert, t }: { cert: CertStatus; t: (key: string, opts?: Record<string, unknown>) => string }) {
  const badgeClass = cert.status === 'issued'
    ? 'badge badge-success'
    : cert.status === 'pending'
    ? 'badge badge-warning'
    : 'badge badge-danger'

  const icon = cert.status === 'issued'
    ? <CheckCircle size={14} color="var(--success)" />
    : cert.status === 'pending'
    ? <AlertCircle size={14} color="var(--warning)" />
    : <XCircle size={14} color="var(--danger)" />

  const statusLabel = t(`adminSettings.certs.status.${cert.status}`)
  const kindLabel = t(`adminSettings.certs.kind.${cert.kind}`)

  // Expiry line: only shown for issued certs. Highlight when <14 days left.
  let expiryLine: string | null = null
  if (cert.status === 'issued' && cert.not_after) {
    const daysLeft = cert.days_left ?? 0
    expiryLine = t('adminSettings.certs.expiresIn', {
      days: daysLeft,
      date: new Date(cert.not_after).toLocaleDateString(),
    })
  }

  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: '10px', padding: '10px 12px', borderRadius: '6px', background: 'var(--bg-base)' }}>
      <div style={{ marginTop: '1px', flexShrink: 0 }}>{icon}</div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' }}>
          <span style={{ fontFamily: MONO, fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', wordBreak: 'break-all' }}>
            {cert.domain}
          </span>
          <span className={badgeClass}>{statusLabel}</span>
          <span style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', letterSpacing: '0.05em', textTransform: 'uppercase' }}>
            {kindLabel}
          </span>
        </div>
        <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>
          {cert.message || ''}
        </div>
        {expiryLine && (
          <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '1px' }}>
            {expiryLine}
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Social OAuth Providers section ───────────────────────────────────────────

type SocialState = {
  google_enabled: string; google_client_id: string; google_client_secret: string; google_redirect_url: string
  discord_enabled: string; discord_client_id: string; discord_client_secret: string; discord_redirect_url: string
  facebook_enabled: string; facebook_client_id: string; facebook_client_secret: string; facebook_redirect_url: string
  twitter_enabled: string; twitter_client_id: string; twitter_client_secret: string; twitter_redirect_url: string
  apple_enabled: string; apple_client_id: string; apple_team_id: string; apple_key_id: string; apple_private_key_p8: string; apple_redirect_url: string
}

const blankSocial: SocialState = {
  google_enabled: 'false', google_client_id: '', google_client_secret: '', google_redirect_url: '',
  discord_enabled: 'false', discord_client_id: '', discord_client_secret: '', discord_redirect_url: '',
  facebook_enabled: 'false', facebook_client_id: '', facebook_client_secret: '', facebook_redirect_url: '',
  twitter_enabled: 'false', twitter_client_id: '', twitter_client_secret: '', twitter_redirect_url: '',
  apple_enabled: 'false', apple_client_id: '', apple_team_id: '', apple_key_id: '', apple_private_key_p8: '', apple_redirect_url: '',
}

type ProviderID = 'google' | 'discord' | 'facebook' | 'twitter' | 'apple'

function pickProviderPatch(p: ProviderID, s: SocialState): Partial<SystemSettings> {
  const out: Record<string, string> = {}
  for (const k of Object.keys(s) as (keyof SocialState)[]) {
    if (k.startsWith(p + '_')) out[k] = s[k]
  }
  return out as Partial<SystemSettings>
}

function ProviderCard({
  id, displayName, social, setSocial, onSave, saving, saved,
}: {
  id: 'google' | 'discord' | 'facebook' | 'twitter'
  displayName: string
  social: SocialState
  setSocial: (next: SocialState) => void
  onSave: (id: ProviderID) => void
  saving: boolean
  saved: boolean
}) {
  const enabled = social[`${id}_enabled` as keyof SocialState] === 'true'
  return (
    <div style={{ border: '1px solid var(--border)', borderRadius: 8, padding: 14, background: 'var(--bg-base)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <span style={{ fontFamily: MONO, fontSize: '0.875rem', fontWeight: 700, color: 'var(--fg-primary)' }}>{displayName}</span>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: '0.8125rem', color: 'var(--fg-muted)', cursor: 'pointer' }}>
          <input type="checkbox" checked={enabled}
            onChange={e => setSocial({ ...social, [`${id}_enabled`]: e.target.checked ? 'true' : 'false' })}
            style={{ accentColor: 'var(--accent)' }} />
          Enabled
        </label>
      </div>
      <Field label="Client ID" value={social[`${id}_client_id` as keyof SocialState]}
        onChange={v => setSocial({ ...social, [`${id}_client_id`]: v })} />
      <Field label="Client Secret" value={social[`${id}_client_secret` as keyof SocialState]}
        onChange={v => setSocial({ ...social, [`${id}_client_secret`]: v })} />
      <Field label="Redirect URL"
        placeholder="https://auth.example.com/_oauth/{id}"
        value={social[`${id}_redirect_url` as keyof SocialState]}
        onChange={v => setSocial({ ...social, [`${id}_redirect_url`]: v })}
        hint="The exact callback URL registered in the provider dashboard." />
      <button className="btn-primary" onClick={() => onSave(id)} disabled={saving}
        style={{ background: saved ? 'var(--success)' : undefined, transition: 'background 300ms' }}>
        {saving ? <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} /> : <Save size={13} />}
        {saved ? 'Saved' : saving ? 'Saving…' : 'Save'}
      </button>
    </div>
  )
}

function AppleProviderCard({
  social, setSocial, onSave, saving, saved,
}: {
  social: SocialState
  setSocial: (next: SocialState) => void
  onSave: (id: ProviderID) => void
  saving: boolean
  saved: boolean
}) {
  const enabled = social.apple_enabled === 'true'
  return (
    <div style={{ border: '1px solid var(--border)', borderRadius: 8, padding: 14, background: 'var(--bg-base)', gridColumn: '1 / -1' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <span style={{ fontFamily: MONO, fontSize: '0.875rem', fontWeight: 700, color: 'var(--fg-primary)' }}>Apple ID</span>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: '0.8125rem', color: 'var(--fg-muted)', cursor: 'pointer' }}>
          <input type="checkbox" checked={enabled}
            onChange={e => setSocial({ ...social, apple_enabled: e.target.checked ? 'true' : 'false' })}
            style={{ accentColor: 'var(--accent)' }} />
          Enabled
        </label>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Field label="Services ID (client_id)"
          value={social.apple_client_id}
          onChange={v => setSocial({ ...social, apple_client_id: v })}
          hint="Apple Developer Service ID, e.g. com.example.muvee.signin" />
        <Field label="Team ID"
          value={social.apple_team_id}
          onChange={v => setSocial({ ...social, apple_team_id: v })}
          hint="10-character Apple Developer Team ID" />
        <Field label="Key ID"
          value={social.apple_key_id}
          onChange={v => setSocial({ ...social, apple_key_id: v })}
          hint="ID of the .p8 sign-in key" />
        <Field label="Redirect URL"
          placeholder="https://auth.example.com/_oauth/apple"
          value={social.apple_redirect_url}
          onChange={v => setSocial({ ...social, apple_redirect_url: v })}
          hint="Must be HTTPS and exactly match the Service ID's Return URLs." />
      </div>
      <div style={{ marginTop: 6, marginBottom: 18 }}>
        <label className="form-label">Private Key (.p8 PEM contents)</label>
        <textarea
          className="form-input"
          rows={6}
          value={social.apple_private_key_p8}
          onChange={e => setSocial({ ...social, apple_private_key_p8: e.target.value })}
          placeholder={'-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----'}
          style={{ fontFamily: MONO, fontSize: '0.8125rem', resize: 'vertical' }}
        />
        <p style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: 4 }}>
          Paste the entire AuthKey_*.p8 file contents. Used to sign the ES256 client_secret JWT on every token exchange.
        </p>
      </div>
      <button className="btn-primary" onClick={() => onSave('apple')} disabled={saving}
        style={{ background: saved ? 'var(--success)' : undefined, transition: 'background 300ms' }}>
        {saving ? <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} /> : <Save size={13} />}
        {saved ? 'Saved' : saving ? 'Saving…' : 'Save'}
      </button>
    </div>
  )
}

function SocialOAuthSection({ initial, t }: { initial: SystemSettings; t: (k: string) => string }) {
  void t // reserved for future i18n keys
  const [social, setSocial] = useState<SocialState>(() => ({
    ...blankSocial,
    google_enabled: initial.google_enabled || 'false',
    google_client_id: initial.google_client_id || '',
    google_client_secret: initial.google_client_secret || '',
    google_redirect_url: initial.google_redirect_url || '',
    discord_enabled: initial.discord_enabled || 'false',
    discord_client_id: initial.discord_client_id || '',
    discord_client_secret: initial.discord_client_secret || '',
    discord_redirect_url: initial.discord_redirect_url || '',
    facebook_enabled: initial.facebook_enabled || 'false',
    facebook_client_id: initial.facebook_client_id || '',
    facebook_client_secret: initial.facebook_client_secret || '',
    facebook_redirect_url: initial.facebook_redirect_url || '',
    twitter_enabled: initial.twitter_enabled || 'false',
    twitter_client_id: initial.twitter_client_id || '',
    twitter_client_secret: initial.twitter_client_secret || '',
    twitter_redirect_url: initial.twitter_redirect_url || '',
    apple_enabled: initial.apple_enabled || 'false',
    apple_client_id: initial.apple_client_id || '',
    apple_team_id: initial.apple_team_id || '',
    apple_key_id: initial.apple_key_id || '',
    apple_private_key_p8: initial.apple_private_key_p8 || '',
    apple_redirect_url: initial.apple_redirect_url || '',
  }))
  const [savingFor, setSavingFor] = useState<ProviderID | null>(null)
  const [savedFor, setSavedFor] = useState<ProviderID | null>(null)

  const save = async (id: ProviderID) => {
    setSavingFor(id)
    try {
      await api.admin.updateSettings(pickProviderPatch(id, social))
      setSavedFor(id)
      setTimeout(() => setSavedFor(curr => (curr === id ? null : curr)), 2000)
    } catch {
      // ignore -- error toast handled by api layer if configured
    } finally {
      setSavingFor(null)
    }
  }

  return (
    <section className="card" style={{ gridColumn: '1 / -1' }}>
      <div className="card-header">Social Login Providers (downstream sign-in)</div>
      <div style={{ padding: 20 }}>
        <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginBottom: 16, lineHeight: 1.6 }}>
          These providers are exposed only on project subdomains (ForwardAuth login pages), not on the muvee platform itself.
          Changes apply live: muvee-server reloads the authservice provider set immediately after save.
        </p>
        <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginBottom: 16, lineHeight: 1.6 }}>
          Google is special: leaving it disabled means downstream falls back to the env-configured platform Google app
          (shared client_id). Enable + fill below to give downstream subdomains a Google Cloud project of their own.
        </p>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: 16 }}>
          <ProviderCard id="google" displayName="Google" social={social} setSocial={setSocial}
            onSave={save} saving={savingFor === 'google'} saved={savedFor === 'google'} />
          <ProviderCard id="discord" displayName="Discord" social={social} setSocial={setSocial}
            onSave={save} saving={savingFor === 'discord'} saved={savedFor === 'discord'} />
          <ProviderCard id="facebook" displayName="Facebook" social={social} setSocial={setSocial}
            onSave={save} saving={savingFor === 'facebook'} saved={savedFor === 'facebook'} />
          <ProviderCard id="twitter" displayName="X (Twitter)" social={social} setSocial={setSocial}
            onSave={save} saving={savingFor === 'twitter'} saved={savedFor === 'twitter'} />
          <AppleProviderCard social={social} setSocial={setSocial}
            onSave={save} saving={savingFor === 'apple'} saved={savedFor === 'apple'} />
        </div>
      </div>
    </section>
  )
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function AdminSettingsPage() {
  const { t } = useTranslation()
  const { refetch: refetchGlobalSettings } = useSettings()

  const [siteName, setSiteName] = useState('')
  const [logoUrl, setLogoUrl] = useState('')
  const [faviconUrl, setFaviconUrl] = useState('')
  const [accessMode, setAccessMode] = useState<AccessMode>('open')
  const [initialSettings, setInitialSettings] = useState<SystemSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [authSaving, setAuthSaving] = useState(false)
  const [authSaved, setAuthSaved] = useState(false)

  const [healthReport, setHealthReport] = useState<HealthReport | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)

  const [certReport, setCertReport] = useState<CertReport | null>(null)
  const [certLoading, setCertLoading] = useState(false)

  const [runtimeConfig, setRuntimeConfig] = useState<RuntimeConfig | null>(null)

  useEffect(() => {
    api.admin.getSettings()
      .then((s: SystemSettings) => {
        setSiteName(s.site_name ?? '')
        setLogoUrl(s.logo_url ?? '')
        setFaviconUrl(s.favicon_url ?? '')
        setAccessMode((s.access_mode || 'open') as AccessMode)
        setInitialSettings(s)
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  const runHealthChecks = useCallback(async () => {
    setHealthLoading(true)
    try {
      const report = await api.admin.health()
      setHealthReport(report)
    } catch {
      // ignore
    } finally {
      setHealthLoading(false)
    }
  }, [])

  useEffect(() => { runHealthChecks() }, [runHealthChecks])

  const loadCerts = useCallback(async () => {
    setCertLoading(true)
    try {
      const report = await api.admin.certs()
      setCertReport(report)
    } catch {
      // ignore
    } finally {
      setCertLoading(false)
    }
  }, [])

  useEffect(() => { loadCerts() }, [loadCerts])

  useEffect(() => {
    api.runtime.config().then(setRuntimeConfig).catch(() => {})
  }, [])

  const saveSettings = async () => {
    setSaving(true)
    try {
      await api.admin.updateSettings({ site_name: siteName, logo_url: logoUrl, favicon_url: faviconUrl })
      refetchGlobalSettings()
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch {
      // ignore
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '40px 0', fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
        <Loader size={14} style={{ animation: 'spin 1s linear infinite' }} />
        {t('common.loading')}
      </div>
    )
  }

  return (
    <div className="page-enter">
      <div className="page-header">
        <p className="page-subtitle">{t('adminSettings.sectionLabel')}</p>
        <h1 className="page-title">{t('adminSettings.heading')}</h1>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px', alignItems: 'start' }}>

        {/* ── Branding ─────────────────────────────────────────────────────── */}
        <section className="card">
          <div className="card-header">{t('adminSettings.branding.title')}</div>
          <div style={{ padding: '20px' }}>
            <Field
              label={t('adminSettings.branding.siteName')}
              value={siteName}
              onChange={setSiteName}
              placeholder="My Private Cloud"
            />
            <ImageField
              label={t('adminSettings.branding.logoUrl')}
              value={logoUrl}
              onChange={setLogoUrl}
              placeholder="https://example.com/logo.png"
              hint={t('adminSettings.branding.logoHint')}
              uploadType="logo"
              t={t}
            />
            <ImageField
              label={t('adminSettings.branding.faviconUrl')}
              value={faviconUrl}
              onChange={setFaviconUrl}
              placeholder="https://example.com/favicon.ico"
              uploadType="favicon"
              t={t}
            />

            <button
              className="btn-primary"
              onClick={saveSettings}
              disabled={saving}
              style={{
                background: saved ? 'var(--success)' : undefined,
                transition: 'background 300ms',
              }}
            >
              {saving ? <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} /> : <Save size={13} />}
              {saved ? t('adminSettings.branding.saved') : saving ? t('adminSettings.branding.saving') : t('adminSettings.branding.save')}
            </button>
          </div>
        </section>

        {/* ── Access control ────────────────────────────────────────────────── */}
        <section className="card">
          <div className="card-header">{t('adminSettings.accessMode.title')}</div>
          <div style={{ padding: '20px' }}>
            <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginBottom: '16px', lineHeight: 1.6 }}>
              {t('adminSettings.accessMode.description')}
            </p>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginBottom: '16px' }}>
              {(['open', 'invite', 'request'] as AccessMode[]).map(mode => (
                <label
                  key={mode}
                  style={{
                    display: 'flex', alignItems: 'flex-start', gap: '10px', cursor: 'pointer',
                    padding: '10px 12px', borderRadius: '6px',
                    background: accessMode === mode ? 'var(--bg-base)' : 'transparent',
                    border: accessMode === mode ? '1px solid var(--accent)' : '1px solid var(--border)',
                  }}
                >
                  <input
                    type="radio"
                    name="access-mode"
                    checked={accessMode === mode}
                    onChange={() => setAccessMode(mode)}
                    style={{ marginTop: '3px', accentColor: 'var(--accent)' }}
                  />
                  <div>
                    <div style={{ fontFamily: MONO, fontSize: '0.8rem', color: 'var(--fg-primary)', fontWeight: 600 }}>
                      {t(`adminSettings.accessMode.modes.${mode}.label`)}
                    </div>
                    <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '2px', lineHeight: 1.5 }}>
                      {t(`adminSettings.accessMode.modes.${mode}.desc`)}
                    </div>
                  </div>
                </label>
              ))}
            </div>
            <button
              className="btn-primary"
              onClick={async () => {
                setAuthSaving(true)
                try {
                  await api.admin.updateSettings({ access_mode: accessMode })
                  refetchGlobalSettings()
                  setAuthSaved(true)
                  setTimeout(() => setAuthSaved(false), 2000)
                } catch {
                  // ignore
                } finally {
                  setAuthSaving(false)
                }
              }}
              disabled={authSaving}
              style={{
                background: authSaved ? 'var(--success)' : undefined,
                transition: 'background 300ms',
              }}
            >
              {authSaving ? <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} /> : <Save size={13} />}
              {authSaved ? t('adminSettings.branding.saved') : authSaving ? t('adminSettings.branding.saving') : t('adminSettings.branding.save')}
            </button>
          </div>
        </section>

        {/* ── Social Login Providers (downstream only) ─────────────────────── */}
        {initialSettings && <SocialOAuthSection initial={initialSettings} t={t} />}

        {/* ── System Health ─────────────────────────────────────────────────── */}
        <section className="card">
          <div className="card-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span>{t('adminSettings.health.title')}</span>
            <button
              className="btn-secondary"
              onClick={runHealthChecks}
              disabled={healthLoading}
              style={{ padding: '4px 10px', fontSize: '0.75rem' }}
            >
              <RefreshCw size={11} style={{ animation: healthLoading ? 'spin 1s linear infinite' : 'none' }} />
              {t('adminSettings.health.recheck')}
            </button>
          </div>
          <div style={{ padding: '16px' }}>
            {healthLoading && !healthReport && (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '12px 0', color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
                <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} />
                {t('adminSettings.health.running')}
              </div>
            )}

            {healthReport && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                {healthReport.checks.map(c => <HealthRow key={c.name} check={c} />)}
                <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '6px', textAlign: 'right' }}>
                  {t('adminSettings.health.updatedAt', { time: new Date(healthReport.updated_at).toLocaleTimeString() })}
                </div>
              </div>
            )}
          </div>
        </section>

        {/* ── Certificates ──────────────────────────────────────────────────── */}
        <section className="card" style={{ gridColumn: '1 / -1' }}>
          <div className="card-header" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <span>{t('adminSettings.certs.title')}</span>
              <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', fontWeight: 400, marginTop: '2px' }}>
                {t('adminSettings.certs.hint')}
              </p>
            </div>
            <button
              className="btn-secondary"
              onClick={loadCerts}
              disabled={certLoading}
              style={{ padding: '4px 10px', fontSize: '0.75rem', flexShrink: 0 }}
            >
              <RefreshCw size={11} style={{ animation: certLoading ? 'spin 1s linear infinite' : 'none' }} />
              {t('adminSettings.certs.recheck')}
            </button>
          </div>
          <div style={{ padding: '16px' }}>
            {certLoading && !certReport && (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '12px 0', color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
                <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} />
                {t('adminSettings.certs.running')}
              </div>
            )}

            {certReport?.store_error && (
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: '8px', padding: '10px 12px', borderRadius: '6px', background: 'var(--bg-base)', marginBottom: '8px' }}>
                <XCircle size={14} color="var(--danger)" style={{ marginTop: '1px', flexShrink: 0 }} />
                <div style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                  {t('adminSettings.certs.storeError', { path: certReport.store_path })}
                  <div style={{ marginTop: '2px', color: 'var(--danger)' }}>{certReport.store_error}</div>
                </div>
              </div>
            )}

            {certReport && certReport.items.length === 0 && !certReport.store_error && (
              <div style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>
                {t('adminSettings.certs.empty')}
              </div>
            )}

            {certReport && certReport.items.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                {certReport.items.map(c => <CertRow key={`${c.kind}-${c.domain}`} cert={c} t={t} />)}
                <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginTop: '6px', textAlign: 'right' }}>
                  {t('adminSettings.certs.updatedAt', { time: new Date(certReport.updated_at).toLocaleTimeString() })}
                </div>
              </div>
            )}
          </div>
        </section>

        {/* ── About ─────────────────────────────────────────────────────────── */}
        <section className="card" style={{ gridColumn: '1 / -1' }}>
          <div className="card-header">{t('adminSettings.about.title')}</div>
          <div style={{ padding: '16px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: '16px', flexWrap: 'wrap' }}>
            <div>
              <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', letterSpacing: '0.05em', textTransform: 'uppercase', marginBottom: '4px' }}>
                {t('adminSettings.about.serverVersion')}
              </div>
              <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                {t('adminSettings.about.versionHint')}
              </div>
            </div>
            <div style={{ fontFamily: MONO, fontSize: '0.9375rem', fontWeight: 600, color: 'var(--fg-primary)', wordBreak: 'break-all' }}>
              {runtimeConfig?.server_version || t('adminSettings.about.unknown')}
            </div>
          </div>
        </section>
      </div>
    </div>
  )
}
