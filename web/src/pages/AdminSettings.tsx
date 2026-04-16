import { useState, useEffect, useCallback, useRef } from 'react'
import { CheckCircle, XCircle, AlertCircle, RefreshCw, Loader, Save, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useSettings } from '../lib/settings'
import type { SystemSettings, HealthReport, CertReport, CertStatus } from '../lib/types'

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

// ─── Main page ────────────────────────────────────────────────────────────────

export default function AdminSettingsPage() {
  const { t } = useTranslation()
  const { refetch: refetchGlobalSettings } = useSettings()

  const [siteName, setSiteName] = useState('')
  const [logoUrl, setLogoUrl] = useState('')
  const [faviconUrl, setFaviconUrl] = useState('')
  const [requireAuthorization, setRequireAuthorization] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [authSaving, setAuthSaving] = useState(false)
  const [authSaved, setAuthSaved] = useState(false)

  const [healthReport, setHealthReport] = useState<HealthReport | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)

  const [certReport, setCertReport] = useState<CertReport | null>(null)
  const [certLoading, setCertLoading] = useState(false)

  useEffect(() => {
    api.admin.getSettings()
      .then((s: SystemSettings) => {
        setSiteName(s.site_name ?? '')
        setLogoUrl(s.logo_url ?? '')
        setFaviconUrl(s.favicon_url ?? '')
        setRequireAuthorization(s.require_authorization === 'true')
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

        {/* ── Authorization ─────────────────────────────────────────────────── */}
        <section className="card">
          <div className="card-header">{t('adminSettings.authorization.title')}</div>
          <div style={{ padding: '20px' }}>
            <p style={{ fontFamily: MONO, fontSize: '0.72rem', color: 'var(--fg-muted)', marginBottom: '16px', lineHeight: 1.6 }}>
              {t('adminSettings.authorization.description')}
            </p>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '16px' }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}>
                <input
                  type="checkbox"
                  checked={requireAuthorization}
                  onChange={e => setRequireAuthorization(e.target.checked)}
                  style={{ width: '16px', height: '16px', accentColor: 'var(--accent)' }}
                />
                <span style={{ fontFamily: MONO, fontSize: '0.8rem', color: 'var(--fg-primary)', fontWeight: 500 }}>
                  {t('adminSettings.authorization.requireLabel')}
                </span>
              </label>
            </div>
            {requireAuthorization && (
              <p style={{ fontFamily: MONO, fontSize: '0.67rem', color: 'var(--fg-muted)', marginBottom: '16px', padding: '8px 10px', background: 'var(--bg-base)', borderRadius: '6px' }}>
                {t('adminSettings.authorization.enabledHint')}
              </p>
            )}
            <button
              className="btn-primary"
              onClick={async () => {
                setAuthSaving(true)
                try {
                  await api.admin.updateSettings({ require_authorization: requireAuthorization ? 'true' : 'false' })
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
      </div>
    </div>
  )
}
