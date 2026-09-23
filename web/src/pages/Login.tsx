import { useEffect, useState, type FormEvent } from 'react'
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
  const [phoneLogin, setPhoneLogin] = useState(false)

  const brandName = settings.site_name || 'muvee'
  const params = new URLSearchParams(window.location.search)
  const cliPort = params.get('port')
  const cliHostname = params.get('hostname')
  const inviteToken = params.get('invite_token')
  const errorCode = params.get('error')
  const redirectTo = params.get('redirect')

  useEffect(() => {
    document.title = `${brandName} — Sign In`
    fetch('/api/auth/providers')
      .then(r => r.json())
      .then((data: { providers: ProviderInfo[]; phone_login: boolean }) => {
        setProviders(data.providers || [])
        setPhoneLogin(!!data.phone_login)
      })
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
                <ProviderButton key={p.id} provider={p} cliPort={cliPort} cliHostname={cliHostname} inviteToken={inviteToken} redirectTo={redirectTo} />
              ))}
            </div>
            {phoneLogin && (
              <>
                {providers.length > 0 && (
                  <div className="flex items-center gap-3 my-4" style={{ color: 'var(--fg-muted)', fontSize: '0.75rem', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                    <span style={{ flex: 1, height: 1, background: 'var(--border)' }} />
                    {t('login.or')}
                    <span style={{ flex: 1, height: 1, background: 'var(--border)' }} />
                  </div>
                )}
                <PhoneLoginForm redirectTo={redirectTo} />
              </>
            )}
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

// PhoneLoginForm is the self-service phone / SMS verification-code login for
// the platform admin plane. Two steps: request a code, then submit phone+code
// to /auth/phone/verify which signs muvee_session on success.
function PhoneLoginForm({ redirectTo }: { redirectTo: string | null }) {
  const { t } = useTranslation()
  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [hint, setHint] = useState('')
  const [countdown, setCountdown] = useState(0)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (countdown <= 0) return
    const id = setTimeout(() => setCountdown(countdown - 1), 1000)
    return () => clearTimeout(id)
  }, [countdown])

  const sendCode = async () => {
    if (!phone) { setHint(t('login.phoneRequired')); return }
    setHint(t('login.sending'))
    try {
      const res = await fetch('/auth/phone/send-code', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ phone }),
      })
      const data = await res.json().catch(() => ({}))
      if (res.ok) { setHint(t('login.codeSent')); setCountdown(60) }
      else { setHint(data.error || t('login.sendFailed')) }
    } catch { setHint(t('login.networkError')) }
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setSubmitting(true); setHint('')
    try {
      const res = await fetch('/auth/phone/verify', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ phone, code }),
      })
      const data = await res.json().catch(() => ({}))
      if (res.ok && data.ok) {
        const safe = redirectTo && redirectTo.startsWith('/') && !redirectTo.startsWith('//')
          ? redirectTo : (data.redirect || '/')
        window.location.href = safe
        return
      }
      setHint(data.error === 'not_invited' ? t('login.errorNotInvited') : t('login.errorInvalidCode'))
    } catch { setHint(t('login.networkError')) }
    setSubmitting(false)
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <input
        value={phone} onChange={e => setPhone(e.target.value)}
        type="tel" autoComplete="tel" placeholder={t('login.phonePlaceholder')}
        className="form-input" required
      />
      <div style={{ display: 'flex', gap: '8px' }}>
        <input
          value={code} onChange={e => setCode(e.target.value)}
          type="text" inputMode="numeric" autoComplete="one-time-code"
          placeholder={t('login.codePlaceholder')}
          className="form-input" style={{ flex: 1 }} required
        />
        <button
          type="button" onClick={sendCode} disabled={countdown > 0}
          className="btn-secondary" style={{ whiteSpace: 'nowrap', padding: '0 12px' }}
        >
          {countdown > 0 ? t('login.resendIn', { s: countdown }) : t('login.sendCode')}
        </button>
      </div>
      <button type="submit" disabled={submitting} className="btn-primary" style={{ width: '100%', padding: '10px 16px' }}>
        {t('login.signIn')}
      </button>
      {hint && <p style={{ fontSize: '0.75rem', color: 'var(--fg-muted)', margin: 0 }}>{hint}</p>}
    </form>
  )
}

function ProviderButton({ provider, cliPort, cliHostname, inviteToken, redirectTo }: { provider: ProviderInfo; cliPort: string | null; cliHostname: string | null; inviteToken: string | null; redirectTo: string | null }) {
  const { t } = useTranslation()
  const inviteQuery = inviteToken ? `&invite_token=${encodeURIComponent(inviteToken)}` : ''
  // CLI device-flow doesn't pass redirect through; redirect only applies to
  // the regular browser login. Constrain to same-origin paths to avoid
  // turning the login URL into an open redirect.
  const safeRedirect = redirectTo && redirectTo.startsWith('/') && !redirectTo.startsWith('//') ? redirectTo : null
  const params = new URLSearchParams()
  if (inviteToken) params.set('invite_token', inviteToken)
  if (safeRedirect) params.set('redirect', safeRedirect)
  const browserQuery = params.toString() ? `?${params.toString()}` : ''
  const href = cliPort
    ? `/auth/cli/login?port=${cliPort}&provider=${provider.id}${cliHostname ? `&hostname=${encodeURIComponent(cliHostname)}` : ''}${inviteQuery}`
    : `/auth/${provider.id}/login${browserQuery}`
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
        <svg width="18" height="18" viewBox="0 0 48 48" fill="none">
          <path d="M17 29C21 29 25 26.9339 28 23.4065C36 14 41.4242 16.8166 44 17.9998C38.5 20.9998 40.5 29.6233 33 35.9998C28.382 39.9259 23.4945 41.014 19 41C12.5231 40.9799 6.86226 37.7637 4 35.4063V16.9998" stroke="#3370FF" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round"/>
          <path d="M5.64808 15.8669C5.02231 14.9567 3.77715 14.7261 2.86694 15.3519C1.95673 15.9777 1.72615 17.2228 2.35192 18.1331L5.64808 15.8669ZM36.0021 35.7309C36.958 35.1774 37.2843 33.9539 36.7309 32.9979C36.1774 32.042 34.9539 31.7157 33.9979 32.2691L36.0021 35.7309ZM2.35192 18.1331C5.2435 22.339 10.7992 28.144 16.8865 32.2239C19.9345 34.2667 23.217 35.946 26.449 36.7324C29.6946 37.522 33.0451 37.4428 36.0021 35.7309L33.9979 32.2691C32.2049 33.3072 29.9929 33.478 27.3947 32.8458C24.783 32.2103 21.9405 30.7958 19.1135 28.9011C13.4508 25.106 8.2565 19.661 5.64808 15.8669L2.35192 18.1331Z" fill="#133C9A"/>
          <path d="M33.5947 17C32.84 14.7027 30.8551 9.94054 27.5947 7H11.5947C15.2174 10.6757 23.0002 16 27.0002 24" stroke="#00D6B9" strokeWidth="4" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      )
    case 'wecom':
      return (
        <svg width="18" height="18" viewBox="0 0 24 24">
          <path d="m17.326 8.158l-.003-.007a6.6 6.6 0 0 0-1.178-1.674c-1.266-1.307-3.067-2.19-5.102-2.417a9.3 9.3 0 0 0-2.124 0h-.001c-2.061.228-3.882 1.107-5.14 2.405a6.7 6.7 0 0 0-1.194 1.682A5.7 5.7 0 0 0 2 10.657c0 1.106.332 2.218.988 3.201l.006.01c.391.594 1.092 1.39 1.637 1.83l.983.793l-.208.875l.527-.267l.708-.358l.761.225c.467.137.955.227 1.517.29h.005q.515.06 1.026.059c.355 0 .724-.02 1.095-.06a9 9 0 0 0 1.346-.258c.095.7.43 1.337.932 1.81c-.658.208-1.352.358-2.061.436c-.442.048-.883.072-1.312.072q-.627 0-1.253-.072a10.7 10.7 0 0 1-1.861-.36l-2.84 1.438s-.29.131-.44.131c-.418 0-.702-.285-.702-.704c0-.252.067-.598.128-.84l.394-1.653c-.728-.586-1.563-1.544-2.052-2.287A7.76 7.76 0 0 1 0 10.658a7.7 7.7 0 0 1 .787-3.39a8.7 8.7 0 0 1 1.551-2.19c1.61-1.665 3.878-2.73 6.359-3.006a11.3 11.3 0 0 1 2.565 0c2.47.275 4.712 1.353 6.323 3.017a8.6 8.6 0 0 1 1.539 2.192c.466.945.769 1.937.769 2.978a3.06 3.06 0 0 0-2-.005c-.001-.644-.189-1.329-.564-2.09zm4.125 6.977l-.024-.024l-.024-.018l-.024-.018l-.096-.095a4.24 4.24 0 0 1-1.169-2.192q0-.038-.006-.075l-.006-.056l-.035-.144a1.3 1.3 0 0 0-.358-.61a1.386 1.386 0 0 0-1.957 0a1.4 1.4 0 0 0 0 1.963c.191.191.418.311.668.371c.024.012.06.012.084.012q.019 0 .041.006q.023.005.042.006a4.24 4.24 0 0 1 2.231 1.186c.048.048.096.095.131.143a.323.323 0 0 0 .466 0a.35.35 0 0 0 .036-.455m-1.05 4.37l-.025.025c-.119.096-.31.096-.453-.036a.326.326 0 0 1 0-.467c.047-.036.094-.083.141-.13l.002-.002a4.27 4.27 0 0 0 1.187-2.28q.005-.024.006-.043c0-.024 0-.06.012-.084a1.386 1.386 0 0 1 2.326-.67a1.4 1.4 0 0 1 0 1.964c-.167.18-.382.299-.608.359l-.143.036l-.057.005q-.035.006-.075.007a4.2 4.2 0 0 0-2.183 1.173l-.095.096q-.009.01-.018.024t-.018.024m-4.392-1.053l.024.024l.024.018q.015.009.024.018l.096.096a4.25 4.25 0 0 1 1.169 2.19q0 .04.006.076q.005.03.006.057l.035.143c.06.228.18.443.358.611c.537.539 1.42.539 1.957 0a1.4 1.4 0 0 0 0-1.964a1.4 1.4 0 0 0-.668-.371c-.024-.012-.06-.012-.084-.012q-.018 0-.041-.006l-.042-.006a4.25 4.25 0 0 1-2.231-1.185a1.4 1.4 0 0 1-.131-.144a.323.323 0 0 0-.466 0a.325.325 0 0 0-.036.455m1.039-4.358l.024-.024a.32.32 0 0 1 .453.035a.326.326 0 0 1 0 .467c-.047.036-.094.083-.141.13l-.002.002a4.27 4.27 0 0 0-1.187 2.281l-.006.042c0 .024 0 .06-.012.084a1.386 1.386 0 0 1-2.326.67a1.4 1.4 0 0 1 0-1.963c.166-.18.381-.3.608-.36l.143-.035q.026 0 .056-.006q.037-.005.075-.006a4.2 4.2 0 0 0 2.183-1.174l.096-.095l.018-.025z" fill="#0082EF"/>
        </svg>
      )
    case 'dingtalk':
      return (
        <svg width="18" height="18" viewBox="0 0 1024 1024">
          <path d="M573.7 252.5C422.5 197.4 201.3 96.7 201.3 96.7c-15.7-4.1-17.9 11.1-17.9 11.1c-5 61.1 33.6 160.5 53.6 182.8c19.9 22.3 319.1 113.7 319.1 113.7S326 357.9 270.5 341.9c-55.6-16-37.9 17.8-37.9 17.8c11.4 61.7 64.9 131.8 107.2 138.4c42.2 6.6 220.1 4 220.1 4s-35.5 4.1-93.2 11.9c-42.7 5.8-97 12.5-111.1 17.8c-33.1 12.5 24 62.6 24 62.6c84.7 76.8 129.7 50.5 129.7 50.5c33.3-10.7 61.4-18.5 85.2-24.2L565 743.1h84.6L603 928l205.3-271.9H700.8l22.3-38.7c.3.5.4.8.4.8S799.8 496.1 829 433.8l.6-1h-.1c5-10.8 8.6-19.7 10-25.8c17-71.3-114.5-99.4-265.8-154.5" fill="#0089FF"/>
        </svg>
      )
    case 'entra':
      // Microsoft's four-square mark (official brand colours).
      return (
        <svg width="18" height="18" viewBox="0 0 23 23">
          <path d="M0 0h11v11H0z" fill="#F25022"/>
          <path d="M12 0h11v11H12z" fill="#7FBA00"/>
          <path d="M0 12h11v11H0z" fill="#00A4EF"/>
          <path d="M12 12h11v11H12z" fill="#FFB900"/>
        </svg>
      )
    case 'slack':
      return (
        <svg width="18" height="18" viewBox="0 0 122.8 122.8">
          <path d="M25.8 77.6c0 7.1-5.8 12.9-12.9 12.9S0 84.7 0 77.6s5.8-12.9 12.9-12.9h12.9v12.9zm6.5 0c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9v32.3c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V77.6z" fill="#E01E5A"/>
          <path d="M45.2 25.8c-7.1 0-12.9-5.8-12.9-12.9S38.1 0 45.2 0s12.9 5.8 12.9 12.9v12.9H45.2zm0 6.5c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H12.9C5.8 58 0 52.2 0 45.1s5.8-12.9 12.9-12.9h32.3z" fill="#36C5F0"/>
          <path d="M97 45.1c0-7.1 5.8-12.9 12.9-12.9s12.9 5.8 12.9 12.9S117 58 109.9 58H97V45.1zm-6.5 0c0 7.1-5.8 12.9-12.9 12.9s-12.9-5.8-12.9-12.9V12.9C64.7 5.8 70.5 0 77.6 0s12.9 5.8 12.9 12.9v32.2z" fill="#2EB67D"/>
          <path d="M77.6 97c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9-12.9-5.8-12.9-12.9V97h12.9zm0-6.5c-7.1 0-12.9-5.8-12.9-12.9s5.8-12.9 12.9-12.9h32.3c7.1 0 12.9 5.8 12.9 12.9s-5.8 12.9-12.9 12.9H77.6z" fill="#ECB22E"/>
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
