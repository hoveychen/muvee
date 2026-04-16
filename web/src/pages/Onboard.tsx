import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { CheckCircle, XCircle, AlertCircle, Loader, ChevronRight, RefreshCw, Server, Globe, HardDrive, Cpu, Copy, Check, Upload } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '../lib/api'
import { useAuth } from '../lib/auth'
import { useSettings } from '../lib/settings'
import type { HealthCheck, HealthReport, Node } from '../lib/types'

const MONO = 'var(--font-mono)'

// ─── Step Indicator ──────────────────────────────────────────────────────────

function StepBar({ current, total }: { current: number; total: number }) {
  return (
    <div style={{ display: 'flex', gap: '8px', marginBottom: '32px' }}>
      {Array.from({ length: total }).map((_, i) => (
        <div key={i} style={{
          height: '3px',
          flex: 1,
          borderRadius: '2px',
          background: i <= current ? 'var(--accent)' : 'var(--bg-hover)',
          opacity: i <= current ? 1 : 0.35,
          transition: 'all 300ms',
        }} />
      ))}
    </div>
  )
}

// ─── Health Check Item ────────────────────────────────────────────────────────

function HintBlock({ hint }: { hint: string }) {
  const [copied, setCopied] = useState(false)
  const { t } = useTranslation()
  return (
    <div style={{
      marginTop: '6px', padding: '8px 10px', borderRadius: '6px',
      background: 'var(--bg-hover)', border: '1px solid var(--border)',
      position: 'relative',
    }}>
      <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', marginBottom: '4px', fontWeight: 600 }}>
        {t('health.fixCommand', 'Fix command')}
      </div>
      <pre style={{
        fontFamily: MONO, fontSize: '0.8125rem', color: 'var(--fg-primary)',
        margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all', lineHeight: 1.5,
      }}>{hint}</pre>
      <button
        onClick={() => { navigator.clipboard.writeText(hint); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
        className="btn-secondary"
        style={{
          position: 'absolute', top: '6px', right: '6px',
          padding: '3px 6px', display: 'flex', alignItems: 'center', gap: '4px',
          fontSize: '0.75rem',
        }}
      >
        {copied ? <Check size={10} /> : <Copy size={10} />}
        {copied ? t('health.copied', 'Copied') : t('health.copy', 'Copy')}
      </button>
    </div>
  )
}

function HealthItem({ check }: { check: HealthCheck }) {
  const { t } = useTranslation()
  const icon = check.status === 'ok'
    ? <CheckCircle size={16} color="var(--success)" />
    : check.status === 'warning'
    ? <AlertCircle size={16} color="var(--warning)" />
    : <XCircle size={16} color="var(--danger)" />

  return (
    <div className="card" style={{
      display: 'flex', alignItems: 'flex-start', gap: '10px',
      padding: '10px 12px',
      borderColor: check.status === 'ok' ? 'rgba(22,163,74,0.25)' : check.status === 'warning' ? 'rgba(217,119,6,0.25)' : 'rgba(220,38,38,0.25)',
    }}>
      <div style={{ marginTop: '1px', flexShrink: 0 }}>{icon}</div>
      <div style={{ flex: 1 }}>
        <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '2px' }}>
          {t(`onboard.checks.${check.name}`, check.name)}
        </div>
        <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
          {check.message}
        </div>
        {check.hint && check.status !== 'ok' && <HintBlock hint={check.hint} />}
      </div>
    </div>
  )
}

// ─── Agent Row ────────────────────────────────────────────────────────────────

function AgentRow({ node }: { node: Node }) {
  const online = Date.now() - new Date(node.last_seen_at).getTime() < 2 * 60 * 1000
  const healthChecks: HealthCheck[] | null = (() => {
    if (!node.health_report) return null
    try {
      // health_report is base64-encoded JSON bytes from Go
      const decoded = atob(node.health_report)
      return JSON.parse(decoded) as HealthCheck[]
    } catch { return null }
  })()

  return (
    <div className="card" style={{
      padding: '12px 14px',
      borderColor: online ? 'rgba(22,163,74,0.3)' : 'var(--border)',
      marginBottom: '8px',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <div style={{ width: '8px', height: '8px', borderRadius: '50%', flexShrink: 0, background: online ? 'var(--success)' : 'var(--fg-muted)' }} className={online ? 'status-running' : ''} />
        <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{node.hostname}</div>
        <span style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', padding: '1px 8px', borderRadius: '6px', background: 'var(--bg-hover)' }}>{node.role}</span>
        <span style={{ fontSize: '0.8125rem', marginLeft: 'auto', color: online ? 'var(--success)' : 'var(--fg-muted)' }}>
          {online ? '● online' : '○ offline'}
        </span>
      </div>
      {healthChecks && (
        <div style={{ marginTop: '8px', display: 'flex', flexWrap: 'wrap', gap: '4px' }}>
          {healthChecks.map(c => (
            <span key={c.name} title={c.hint ? `${c.message}\n\nFix: ${c.hint}` : c.message} style={{
              fontSize: '0.75rem', padding: '2px 7px', borderRadius: '6px',
              background: c.status === 'ok' ? 'rgba(22,163,74,0.12)' : c.status === 'warning' ? 'rgba(217,119,6,0.12)' : 'rgba(220,38,38,0.12)',
              color: c.status === 'ok' ? 'var(--success)' : c.status === 'warning' ? 'var(--warning)' : 'var(--danger)',
              border: `1px solid ${c.status === 'ok' ? 'rgba(22,163,74,0.3)' : c.status === 'warning' ? 'rgba(217,119,6,0.3)' : 'rgba(220,38,38,0.3)'}`,
              cursor: 'help',
            }}>
              {c.name}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Main ─────────────────────────────────────────────────────────────────────

const TOTAL_STEPS = 4

export default function OnboardPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { user } = useAuth()
  const { refetch: refetchSettings } = useSettings()
  const [step, setStep] = useState(0)

  // Only admins can access the onboard flow
  useEffect(() => {
    if (user && user.role !== 'admin') {
      navigate('/projects', { replace: true })
    }
  }, [user, navigate])

  // Step 1 – Branding
  const [siteName, setSiteName] = useState('')
  const [logoUrl, setLogoUrl] = useState('')
  const [faviconUrl, setFaviconUrl] = useState('')
  const [brandingSaving, setBrandingSaving] = useState(false)

  // Step 2 – Health
  const [healthReport, setHealthReport] = useState<HealthReport | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)

  // Step 3 – Agents
  const [nodes, setNodes] = useState<Node[]>([])
  const [nodesLoading, setNodesLoading] = useState(false)

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

  const refreshNodes = useCallback(async () => {
    setNodesLoading(true)
    try {
      const ns = await api.nodes.list()
      setNodes(ns)
    } catch {
      // ignore
    } finally {
      setNodesLoading(false)
    }
  }, [])

  // Auto-run checks when entering each step
  useEffect(() => {
    if (step === 1) runHealthChecks()
    if (step === 2) refreshNodes()
  }, [step, runHealthChecks, refreshNodes])

  // Polling for agents on step 2
  useEffect(() => {
    if (step !== 2) return
    const id = setInterval(refreshNodes, 5000)
    return () => clearInterval(id)
  }, [step, refreshNodes])

  const healthOK = healthReport?.checks.every(c => c.status !== 'error') ?? false

  const onlineBuilders = nodes.filter(n => n.role === 'builder' && Date.now() - new Date(n.last_seen_at).getTime() < 2 * 60 * 1000)
  const onlineDeployers = nodes.filter(n => n.role === 'deploy' && Date.now() - new Date(n.last_seen_at).getTime() < 2 * 60 * 1000)
  const agentsReady = onlineBuilders.length > 0 && onlineDeployers.length > 0

  const saveBranding = async () => {
    setBrandingSaving(true)
    try {
      await api.admin.updateSettings({
        site_name: siteName,
        logo_url: logoUrl,
        favicon_url: faviconUrl,
      })
      refetchSettings()
      setStep(1)
    } catch {
      // ignore
    } finally {
      setBrandingSaving(false)
    }
  }

  const completeOnboarding = async () => {
    await api.admin.updateSettings({ onboarded: 'true' })
    await refetchSettings()
    navigate('/projects')
  }

  const field = (label: string, value: string, onChange: (v: string) => void, placeholder?: string) => (
    <div style={{ marginBottom: '16px' }}>
      <label className="form-label">
        {label}
      </label>
      <input
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        className="form-input"
      />
    </div>
  )

  const ImageField = useCallback(({ label, value, onChange, uploadType, placeholder }: { label: string; value: string; onChange: (v: string) => void; uploadType: 'logo' | 'favicon'; placeholder?: string }) => {
    const inputRef = useRef<HTMLInputElement>(null)
    const [uploading, setUploading] = useState(false)
    const handleUpload = async (file: File) => {
      setUploading(true)
      try {
        const result = await api.admin.uploadBranding(uploadType, file)
        onChange(result.url)
      } catch { /* ignore */ }
      finally { setUploading(false) }
    }
    return (
      <div style={{ marginBottom: '16px' }}>
        <label className="form-label">
          {label}
        </label>
        <div style={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
          <input
            value={value}
            onChange={e => onChange(e.target.value)}
            placeholder={placeholder}
            className="form-input"
            style={{ flex: 1 }}
          />
          <button
            onClick={() => inputRef.current?.click()}
            disabled={uploading}
            className="btn-secondary"
            style={{
              display: 'flex', alignItems: 'center', gap: '4px',
              padding: '8px 12px', flexShrink: 0,
              cursor: uploading ? 'default' : 'pointer',
            }}
          >
            {uploading ? <Loader size={12} style={{ animation: 'spin 1s linear infinite' }} /> : <Upload size={12} />}
            {t('onboard.branding.upload')}
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
        {value && (
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '8px' }}>
            <img src={value} alt="" style={{ width: '32px', height: '32px', borderRadius: '6px', objectFit: 'contain', border: '1px solid var(--border)' }} />
            <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>{t('onboard.branding.logoPreview')}</span>
          </div>
        )}
      </div>
    )
  }, [t])

  const btn = (label: string, onClick: () => void, disabled = false, loading = false) => (
    <button
      onClick={onClick}
      disabled={disabled || loading}
      className="btn-primary"
      style={{
        display: 'flex', alignItems: 'center', gap: '6px',
        cursor: disabled ? 'default' : 'pointer',
        opacity: disabled ? 0.5 : 1,
      }}
    >
      {loading && <Loader size={14} style={{ animation: 'spin 1s linear infinite' }} />}
      {label}
      {!loading && <ChevronRight size={14} />}
    </button>
  )

  const iconForCheck = (name: string) => {
    if (name.includes('nfs') || name.includes('git_repo')) return <HardDrive size={13} />
    if (name.includes('agent')) return <Server size={13} />
    if (name.includes('registry') || name.includes('traefik') || name.includes('internet')) return <Globe size={13} />
    return <Cpu size={13} />
  }

  return (
    <div style={{
      minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center',
      background: 'var(--bg-base)', padding: '40px 16px',
    }}>
      <div style={{ width: '100%', maxWidth: '580px' }}>
        {/* Header */}
        <div style={{ marginBottom: '32px' }}>
          <div className="page-subtitle" style={{ marginBottom: '8px' }}>
            {t('onboard.stepLabel', { current: step + 1, total: TOTAL_STEPS })}
          </div>
          <h1 className="page-title">
            {t(`onboard.steps.${step}.title`)}
          </h1>
          <p style={{ marginTop: '6px', color: 'var(--fg-muted)', fontSize: '0.9rem', lineHeight: 1.55 }}>
            {t(`onboard.steps.${step}.desc`)}
          </p>
        </div>

        <StepBar current={step} total={TOTAL_STEPS} />

        {/* ── Step 0: Branding ─────────────────────────────────────────────── */}
        {step === 0 && (
          <div className="page-enter">
            {field(t('onboard.branding.siteName'), siteName, setSiteName, 'My Private Cloud')}
            <ImageField label={t('onboard.branding.logoUrl')} value={logoUrl} onChange={setLogoUrl} uploadType="logo" placeholder="https://example.com/logo.png" />
            <ImageField label={t('onboard.branding.faviconUrl')} value={faviconUrl} onChange={setFaviconUrl} uploadType="favicon" placeholder="https://example.com/favicon.ico" />

            <div style={{ display: 'flex', gap: '10px', alignItems: 'center', marginTop: '24px' }}>
              {btn(t('onboard.branding.save'), saveBranding, brandingSaving, brandingSaving)}
              <button
                onClick={() => setStep(1)}
                className="btn-secondary"
              >
                {t('onboard.skip')}
              </button>
            </div>
          </div>
        )}

        {/* ── Step 1: System Health ─────────────────────────────────────────── */}
        {step === 1 && (
          <div className="page-enter">
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: '14px' }}>
              <button
                onClick={runHealthChecks}
                disabled={healthLoading}
                className="btn-secondary"
                style={{
                  display: 'flex', alignItems: 'center', gap: '5px',
                }}
              >
                <RefreshCw size={12} style={{ animation: healthLoading ? 'spin 1s linear infinite' : 'none' }} />
                {t('onboard.health.recheck')}
              </button>
            </div>

            {healthLoading && !healthReport && (
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', padding: '20px 0', color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
                <Loader size={14} style={{ animation: 'spin 1s linear infinite' }} />
                {t('onboard.health.running')}
              </div>
            )}

            {healthReport && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginBottom: '24px' }}>
                {healthReport.checks.map(c => (
                  <div key={c.name} className="card" style={{
                    display: 'flex', alignItems: 'flex-start', gap: '10px',
                    padding: '10px 12px',
                    borderColor: c.status === 'ok' ? 'rgba(22,163,74,0.2)' : c.status === 'warning' ? 'rgba(217,119,6,0.2)' : 'rgba(220,38,38,0.2)',
                  }}>
                    <div style={{ marginTop: '1px', flexShrink: 0, color: c.status === 'ok' ? 'var(--success)' : c.status === 'warning' ? 'var(--warning)' : 'var(--danger)' }}>
                      {c.status === 'ok' ? <CheckCircle size={15} /> : c.status === 'warning' ? <AlertCircle size={15} /> : <XCircle size={15} />}
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flex: 1 }}>
                      <span style={{ color: 'var(--fg-muted)', flexShrink: 0 }}>{iconForCheck(c.name)}</span>
                      <div>
                        <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--fg-primary)' }}>
                          {t(`onboard.checks.${c.name}`, { defaultValue: c.name })}
                        </div>
                        <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', marginTop: '2px' }}>{c.message}</div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}

            {healthReport && !healthOK && (
              <div className="card" style={{
                padding: '10px 14px',
                background: 'rgba(220,38,38,0.08)', borderColor: 'rgba(220,38,38,0.3)',
                fontSize: '0.875rem', color: 'var(--fg-muted)',
                marginBottom: '20px',
              }}>
                {t('onboard.health.hasErrors')}
              </div>
            )}

            <div style={{ display: 'flex', gap: '10px', marginTop: '8px' }}>
              {btn(t('onboard.next'), () => setStep(2), false, false)}
              {step > 0 && (
                <button onClick={() => setStep(s => s - 1)} className="btn-secondary" style={{ padding: '9px 16px' }}>
                  {t('onboard.back')}
                </button>
              )}
            </div>
          </div>
        )}

        {/* ── Step 2: Agent Setup ───────────────────────────────────────────── */}
        {step === 2 && (
          <div className="page-enter">
            {/* Instructions card */}
            <div className="card" style={{ padding: '16px', marginBottom: '20px', fontSize: '0.875rem', lineHeight: 1.6 }}>
              <div style={{ color: 'var(--fg-muted)', marginBottom: '10px', fontSize: '0.8125rem', letterSpacing: '0.06em' }}>
                {t('onboard.agents.instructions')}
              </div>
              <div style={{ color: 'var(--fg-primary)', fontWeight: 600, marginBottom: '6px' }}>1. {t('onboard.agents.step1')}</div>
              <pre style={{ fontFamily: MONO, background: 'var(--bg-base)', padding: '8px 12px', borderRadius: '6px', fontSize: '0.8125rem', overflowX: 'auto', color: 'var(--fg-primary)', margin: '0 0 12px' }}>
                {`NODE_ROLE=builder CONTROL_PLANE_URL=<server_url> \\\n  AGENT_SECRET=<agent_secret> \\\n  ./muvee agent`}
              </pre>
              <div style={{ color: 'var(--fg-primary)', fontWeight: 600, marginBottom: '6px' }}>2. {t('onboard.agents.step2')}</div>
              <pre style={{ fontFamily: MONO, background: 'var(--bg-base)', padding: '8px 12px', borderRadius: '6px', fontSize: '0.8125rem', overflowX: 'auto', color: 'var(--fg-primary)', margin: '0 0 4px' }}>
                {`NODE_ROLE=deploy CONTROL_PLANE_URL=<server_url> \\\n  AGENT_SECRET=<agent_secret> \\\n  ./muvee agent`}
              </pre>
            </div>

            {/* Status */}
            <div style={{ display: 'flex', gap: '12px', marginBottom: '16px' }}>
              <div className="card" style={{
                flex: 1, padding: '10px 14px',
                background: onlineBuilders.length > 0 ? 'rgba(22,163,74,0.08)' : 'var(--bg-card)',
                borderColor: onlineBuilders.length > 0 ? 'rgba(22,163,74,0.3)' : 'var(--border)',
                display: 'flex', alignItems: 'center', gap: '8px',
              }}>
                {onlineBuilders.length > 0
                  ? <CheckCircle size={15} color="var(--success)" />
                  : <div style={{ width: '15px', height: '15px', borderRadius: '50%', border: '2px solid var(--fg-muted)', animation: 'spin 2s linear infinite' }} />
                }
                <div>
                  <div style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{t('onboard.agents.builder')}</div>
                  <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                    {onlineBuilders.length > 0 ? `${onlineBuilders.length} online` : t('onboard.agents.waiting')}
                  </div>
                </div>
              </div>
              <div className="card" style={{
                flex: 1, padding: '10px 14px',
                background: onlineDeployers.length > 0 ? 'rgba(22,163,74,0.08)' : 'var(--bg-card)',
                borderColor: onlineDeployers.length > 0 ? 'rgba(22,163,74,0.3)' : 'var(--border)',
                display: 'flex', alignItems: 'center', gap: '8px',
              }}>
                {onlineDeployers.length > 0
                  ? <CheckCircle size={15} color="var(--success)" />
                  : <div style={{ width: '15px', height: '15px', borderRadius: '50%', border: '2px solid var(--fg-muted)', animation: 'spin 2s linear infinite' }} />
                }
                <div>
                  <div style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--fg-primary)' }}>{t('onboard.agents.deployer')}</div>
                  <div style={{ fontSize: '0.75rem', color: 'var(--fg-muted)' }}>
                    {onlineDeployers.length > 0 ? `${onlineDeployers.length} online` : t('onboard.agents.waiting')}
                  </div>
                </div>
              </div>
            </div>

            {/* Node list */}
            {nodes.length > 0 && (
              <div style={{ marginBottom: '20px' }}>
                <div style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)', letterSpacing: '0.06em', marginBottom: '8px' }}>
                  {t('onboard.agents.registered')}
                </div>
                {nodes.map(n => <AgentRow key={n.id} node={n} />)}
              </div>
            )}

            <div style={{ display: 'flex', gap: '10px', marginTop: '8px', alignItems: 'center' }}>
              {btn(
                agentsReady ? t('onboard.next') : t('onboard.agents.continue'),
                () => setStep(3),
                false,
                nodesLoading,
              )}
              <button onClick={() => setStep(s => s - 1)} className="btn-secondary" style={{ padding: '9px 16px' }}>
                {t('onboard.back')}
              </button>
              {!agentsReady && (
                <span style={{ fontSize: '0.8125rem', color: 'var(--fg-muted)' }}>
                  {t('onboard.agents.orSkip')}
                </span>
              )}
            </div>
          </div>
        )}

        {/* ── Step 3: Done ─────────────────────────────────────────────────── */}
        {step === 3 && (
          <div className="page-enter">
            <div className="card" style={{
              padding: '24px',
              background: 'rgba(22,163,74,0.06)', borderColor: 'rgba(22,163,74,0.3)',
              marginBottom: '28px', textAlign: 'center',
            }}>
              <CheckCircle size={40} color="var(--success)" style={{ marginBottom: '12px' }} />
              <div style={{ fontSize: '1.1rem', fontWeight: 600, color: 'var(--fg-primary)', marginBottom: '6px' }}>
                {t('onboard.done.heading')}
              </div>
              <div style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
                {t('onboard.done.desc')}
              </div>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', marginBottom: '24px' }}>
              {[
                t('onboard.done.feat1'),
                t('onboard.done.feat2'),
                t('onboard.done.feat3'),
              ].map((f, i) => (
                <div key={i} style={{ display: 'flex', gap: '8px', alignItems: 'flex-start' }}>
                  <CheckCircle size={14} color="var(--success)" style={{ marginTop: '2px', flexShrink: 0 }} />
                  <span style={{ fontSize: '0.875rem', color: 'var(--fg-muted)' }}>{f}</span>
                </div>
              ))}
            </div>

            {btn(t('onboard.done.enter'), completeOnboarding)}
          </div>
        )}
      </div>
    </div>
  )
}
