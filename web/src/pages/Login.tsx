import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSettings } from '../lib/settings'

interface ProviderInfo {
  id: string
  display_name: string
}

export default function LoginPage() {
  const { t } = useTranslation()
  const { settings } = useSettings()
  const [providers, setProviders] = useState<ProviderInfo[]>([])

  const brandName = settings.site_name || 'muvee'
  const params = new URLSearchParams(window.location.search)
  const cliPort = params.get('port')
  const cliHostname = params.get('hostname')
  const inviteToken = params.get('invite_token')
  const errorCode = params.get('error')

  useEffect(() => {
    document.title = `${brandName} — Sign In`
    fetch('/api/auth/providers')
      .then(r => r.json())
      .then((data: ProviderInfo[]) => setProviders(data))
      .catch(() => {})
  }, [brandName])

  const errorMessage = (() => {
    switch (errorCode) {
      case 'not_invited': return t('login.errorNotInvited')
      case 'domain_not_allowed': return t('login.errorDomainNotAllowed')
      case 'oauth_failed': return t('login.errorOAuthFailed')
      default: return errorCode ? t('login.errorGeneric') : ''
    }
  })()

  return (
    <div className="min-h-screen flex" style={{ background: 'var(--bg-base)' }}>
      {/* Left panel */}
      <div
        className="hidden lg:flex flex-col justify-between w-1/2 p-16 relative overflow-hidden"
        style={{ background: 'var(--sidebar-bg)' }}
      >
        <div className="relative z-10">
          <span
            className="text-xs tracking-widest uppercase"
            style={{ color: 'var(--sidebar-fg)', fontSize: '0.8125rem' }}
          >
            {t('login.tagline')}
          </span>
        </div>

        <div className="relative z-10">
          {settings.logo_url ? (
            <img src={settings.logo_url} alt={brandName} style={{ height: '80px', objectFit: 'contain' }} />
          ) : (
            <h1
              className="font-bold leading-none tracking-tight"
              style={{ fontSize: '6rem', color: '#ffffff', lineHeight: '1' }}
            >
              {brandName}
            </h1>
          )}
          <p
            className="mt-6 text-lg max-w-sm"
            style={{ color: 'var(--sidebar-fg)', lineHeight: '1.7', whiteSpace: 'pre-line', fontSize: '1rem' }}
          >
            {t('login.description')}
          </p>
        </div>

        <div className="relative z-10 flex gap-6" style={{ color: '#e2e8f0', fontSize: '0.875rem' }}>
          <span>{t('login.feat1')}</span>
          <span style={{ color: 'var(--sidebar-border)' }}>·</span>
          <span>{t('login.feat2')}</span>
          <span style={{ color: 'var(--sidebar-border)' }}>·</span>
          <span>{t('login.feat3')}</span>
        </div>
      </div>

      {/* Right panel */}
      <div className="flex-1 flex flex-col items-center justify-center p-8 lg:p-16">
        <div
          className="w-full max-w-sm page-enter"
          style={{ animation: 'page-enter 300ms ease-out' }}
        >
          {/* Mobile logo */}
          <div className="lg:hidden mb-10">
            {settings.logo_url ? (
              <img src={settings.logo_url} alt={brandName} style={{ height: '48px', objectFit: 'contain' }} />
            ) : (
              <h1
                className="text-5xl font-bold"
                style={{ color: 'var(--fg-primary)' }}
              >
                {brandName}
              </h1>
            )}
          </div>

          <div
            className="card"
            style={{ padding: '32px' }}
          >
            <h2
              className="font-semibold mb-1"
              style={{ color: 'var(--fg-primary)', fontSize: '1.25rem' }}
            >
              {t('login.signIn')}
            </h2>
            <p
              className="mb-8"
              style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}
            >
              {t('login.authorizedOnly')}
            </p>

            {cliPort && (
              <p className="mb-4 px-3 py-2 rounded" style={{ fontFamily: 'var(--font-mono)', color: 'var(--fg-muted)', background: 'var(--bg-hover)', border: '1px solid var(--border)', fontSize: '0.8125rem' }}>
                {t('login.cliAuthPrompt')}
              </p>
            )}
            {errorMessage && (
              <p className="mb-4 px-3 py-2 rounded" style={{ color: 'var(--danger)', background: 'var(--bg-hover)', border: '1px solid var(--danger)', fontSize: '0.8125rem' }}>
                {errorMessage}
              </p>
            )}
            {inviteToken && !errorMessage && (
              <p className="mb-4 px-3 py-2 rounded" style={{ color: 'var(--fg-muted)', background: 'var(--bg-hover)', border: '1px solid var(--accent)', fontSize: '0.8125rem' }}>
                {t('login.invitePresent')}
              </p>
            )}
            <div className="flex flex-col gap-3">
              {providers.map(p => (
                <ProviderButton key={p.id} provider={p} cliPort={cliPort} cliHostname={cliHostname} inviteToken={inviteToken} />
              ))}
            </div>
          </div>

          <p
            className="text-center mt-5"
            style={{ color: 'var(--fg-muted)', fontSize: '0.8125rem' }}
          >
            {t('login.accessRestricted')}
          </p>
        </div>
      </div>
    </div>
  )
}

function ProviderButton({ provider, cliPort, cliHostname, inviteToken }: { provider: ProviderInfo; cliPort: string | null; cliHostname: string | null; inviteToken: string | null }) {
  const { t } = useTranslation()
  const inviteQuery = inviteToken ? `&invite_token=${encodeURIComponent(inviteToken)}` : ''
  const inviteQueryFirst = inviteToken ? `?invite_token=${encodeURIComponent(inviteToken)}` : ''
  const href = cliPort
    ? `/auth/cli/login?port=${cliPort}&provider=${provider.id}${cliHostname ? `&hostname=${encodeURIComponent(cliHostname)}` : ''}${inviteQuery}`
    : `/auth/${provider.id}/login${inviteQueryFirst}`
  return (
    <a
      href={href}
      className="btn-secondary"
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '10px',
        width: '100%',
        padding: '10px 16px',
        fontSize: '0.875rem',
      }}
      onMouseEnter={e => {
        (e.currentTarget as HTMLAnchorElement).style.borderColor = 'var(--accent)'
      }}
      onMouseLeave={e => {
        (e.currentTarget as HTMLAnchorElement).style.borderColor = 'var(--border)'
      }}
    >
      <ProviderIcon id={provider.id} />
      {t('login.continueWith', { provider: provider.display_name })}
    </a>
  )
}

function ProviderIcon({ id }: { id: string }) {
  switch (id) {
    case 'google':
      return (
        <svg width="18" height="18" viewBox="0 0 24 24">
          <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/>
          <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
          <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
          <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
        </svg>
      )
    case 'feishu':
      return (
        <svg width="18" height="18" viewBox="0 0 64 64" fill="none">
          <rect width="64" height="64" rx="14" fill="#1456F0"/>
          <path d="M20 44L32 20L44 44" stroke="white" strokeWidth="5" strokeLinecap="round" strokeLinejoin="round"/>
          <path d="M24 36H40" stroke="white" strokeWidth="5" strokeLinecap="round"/>
        </svg>
      )
    case 'wecom':
      return (
        <svg width="18" height="18" viewBox="0 0 64 64" fill="none">
          <rect width="64" height="64" rx="14" fill="#1AAD19"/>
          <path d="M26 28c-5.523 0-10 3.806-10 8.5 0 2.619 1.37 4.96 3.527 6.534L18 47l3.945-1.577C23.193 45.79 24.559 46 26 46c5.523 0 10-3.806 10-8.5S31.523 28 26 28z" fill="white"/>
          <path d="M38 18c-6.627 0-12 4.477-12 10 0 3.074 1.59 5.833 4.1 7.712L29 40l4.72-1.888C35.064 38.684 36.5 39 38 39c6.627 0 12-4.477 12-10S44.627 18 38 18z" fill="white" fillOpacity="0.9"/>
        </svg>
      )
    case 'dingtalk':
      return (
        <svg width="18" height="18" viewBox="0 0 64 64" fill="none">
          <rect width="64" height="64" rx="14" fill="#1677FF"/>
          <path d="M32 14C21.507 14 13 22.507 13 33s8.507 19 19 19 19-8.507 19-19S42.493 14 32 14zm8.5 26.5l-10-5.5V22h3v11.5l8 4.5-1 2.5z" fill="white"/>
        </svg>
      )
    default:
      return (
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10"/>
          <path d="M12 8v4m0 4h.01"/>
        </svg>
      )
  }
}
