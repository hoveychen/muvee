import { useState, useEffect, useCallback } from 'react'
import { CheckCircle, XCircle, AlertCircle, RefreshCw, Loader, Save } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useSettings } from '../lib/settings'
import type { SystemSettings, HealthReport } from '../lib/types'

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
      <label style={{ display: 'block', fontFamily: MONO, fontSize: '0.68rem', color: 'var(--fg-muted)', letterSpacing: '0.06em', marginBottom: '6px' }}>
        {label}
      </label>
      <input
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        style={{
          width: '100%', padding: '8px 10px', boxSizing: 'border-box',
          background: 'var(--bg-base)', border: '1px solid var(--border)',
          borderRadius: '6px', color: 'var(--fg-primary)',
          fontFamily: MONO, fontSize: '0.85rem', outline: 'none',
        }}
      />
      {hint && <p style={{ fontFamily: MONO, fontSize: '0.67rem', color: 'var(--fg-muted)', marginTop: '4px' }}>{hint}</p>}
    </div>
  )
}

// ─── Health check row ─────────────────────────────────────────────────────────

function HealthRow({ check }: { check: import('../lib/types').HealthCheck }) {
  const icon = check.status === 'ok'
    ? <CheckCircle size={14} color="#3fb950" />
    : check.status === 'warning'
    ? <AlertCircle size={14} color="#d29922" />
    : <XCircle size={14} color="var(--danger)" />

  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: '8px', padding: '8px 10px', borderRadius: '5px', background: 'var(--bg-base)' }}>
      <div style={{ marginTop: '1px', flexShrink: 0 }}>{icon}</div>
      <div>
        <div style={{ fontFamily: MONO, fontSize: '0.72rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{check.name}</div>
        <div style={{ fontFamily: MONO, fontSize: '0.67rem', color: 'var(--fg-muted)', marginTop: '1px' }}>{check.message}</div>
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
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  const [healthReport, setHealthReport] = useState<HealthReport | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)

  useEffect(() => {
    api.admin.getSettings()
      .then((s: SystemSettings) => {
        setSiteName(s.site_name ?? '')
        setLogoUrl(s.logo_url ?? '')
        setFaviconUrl(s.favicon_url ?? '')
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
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '40px 0', fontFamily: MONO, fontSize: '0.8rem', color: 'var(--fg-muted)' }}>
        <Loader size={14} style={{ animation: 'spin 1s linear infinite' }} />
        {t('common.loading')}
      </div>
    )
  }

  return (
    <div className="page-enter">
      <div style={{ marginBottom: '32px' }}>
        <p style={{ fontFamily: MONO, color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.05em' }}>{t('adminSettings.sectionLabel')}</p>
        <h1 style={{ fontSize: '1.75rem', fontWeight: 700, color: 'var(--fg-primary)', lineHeight: 1.2, marginTop: '4px' }}>{t('adminSettings.heading')}</h1>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px', alignItems: 'start' }}>

        {/* ── Branding ─────────────────────────────────────────────────────── */}
        <section style={{ border: '1px solid var(--border)', borderRadius: '8px', padding: '20px', background: 'var(--bg-card)' }}>
          <h2 style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '18px' }}>{t('adminSettings.branding.title')}</h2>

          <Field
            label={t('adminSettings.branding.siteName')}
            value={siteName}
            onChange={setSiteName}
            placeholder="My Private Cloud"
          />
          <Field
            label={t('adminSettings.branding.logoUrl')}
            value={logoUrl}
            onChange={setLogoUrl}
            placeholder="https://example.com/logo.png"
            hint={t('adminSettings.branding.logoHint')}
          />
          <Field
            label={t('adminSettings.branding.faviconUrl')}
            value={faviconUrl}
            onChange={setFaviconUrl}
            placeholder="https://example.com/favicon.ico"
          />

          {logoUrl && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '18px' }}>
              <img
                src={logoUrl} alt=""
                style={{ width: '36px', height: '36px', borderRadius: '6px', objectFit: 'contain', border: '1px solid var(--border)' }}
              />
              <span style={{ fontFamily: MONO, fontSize: '0.7rem', color: 'var(--fg-muted)' }}>{t('adminSettings.branding.preview')}</span>
            </div>
          )}

          <button
            onClick={saveSettings}
            disabled={saving}
            style={{
              display: 'flex', alignItems: 'center', gap: '6px',
              padding: '8px 18px',
              background: saved ? '#3fb950' : 'var(--accent)',
              color: '#fff', border: 'none', borderRadius: '6px',
              fontFamily: MONO, fontSize: '0.82rem', fontWeight: 600,
              cursor: saving ? 'default' : 'pointer',
              transition: 'background 300ms',
            }}
          >
            {saving ? <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} /> : <Save size={13} />}
            {saved ? t('adminSettings.branding.saved') : saving ? t('adminSettings.branding.saving') : t('adminSettings.branding.save')}
          </button>
        </section>

        {/* ── System Health ─────────────────────────────────────────────────── */}
        <section style={{ border: '1px solid var(--border)', borderRadius: '8px', padding: '20px', background: 'var(--bg-card)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '16px' }}>
            <h2 style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{t('adminSettings.health.title')}</h2>
            <button
              onClick={runHealthChecks}
              disabled={healthLoading}
              style={{
                display: 'flex', alignItems: 'center', gap: '4px',
                background: 'none', border: '1px solid var(--border)', borderRadius: '5px',
                padding: '4px 10px', fontFamily: MONO, fontSize: '0.68rem', color: 'var(--fg-muted)',
                cursor: 'pointer',
              }}
            >
              <RefreshCw size={11} style={{ animation: healthLoading ? 'spin 1s linear infinite' : 'none' }} />
              {t('adminSettings.health.recheck')}
            </button>
          </div>

          {healthLoading && !healthReport && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '12px 0', color: 'var(--fg-muted)', fontFamily: MONO, fontSize: '0.75rem' }}>
              <Loader size={13} style={{ animation: 'spin 1s linear infinite' }} />
              {t('adminSettings.health.running')}
            </div>
          )}

          {healthReport && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
              {healthReport.checks.map(c => <HealthRow key={c.name} check={c} />)}
              <div style={{ fontFamily: MONO, fontSize: '0.62rem', color: 'var(--fg-muted)', marginTop: '6px', textAlign: 'right' }}>
                {t('adminSettings.health.updatedAt', { time: new Date(healthReport.updated_at).toLocaleTimeString() })}
              </div>
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
