import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { useAuth } from '../lib/auth'
import type { ProjectAccessRequest, ProjectInfo } from '../lib/types'

type Phase = 'loading' | 'form' | 'submitted' | 'already-allowed' | 'error'

export default function RequestAccess() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { user, loading: authLoading } = useAuth()
  const projectId = params.get('project') ?? ''
  const [phase, setPhase] = useState<Phase>('loading')
  const [project, setProject] = useState<ProjectInfo | null>(null)
  const [reason, setReason] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitted, setSubmitted] = useState<ProjectAccessRequest | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (authLoading) return
    if (!user) {
      const here = `/request-access?project=${encodeURIComponent(projectId)}`
      navigate(`/login?redirect=${encodeURIComponent(here)}`, { replace: true })
      return
    }
    if (!projectId) {
      setPhase('error')
      setError('Missing ?project=<id> in the URL.')
      return
    }
    api.projects.info(projectId)
      .then(p => { setProject(p); setPhase('form') })
      .catch(e => { setPhase('error'); setError(e instanceof Error ? e.message : String(e)) })
  }, [authLoading, user, projectId, navigate])

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      const req = await api.projects.submitAccessRequest(projectId, reason)
      setSubmitted(req)
      setPhase('submitted')
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      if (/already has access/i.test(msg)) {
        setPhase('already-allowed')
      } else {
        setError(msg)
      }
    } finally {
      setBusy(false)
    }
  }

  const card: React.CSSProperties = {
    background: 'var(--bg-card)',
    border: '1px solid var(--border)',
    borderRadius: 8,
    padding: '2rem',
    maxWidth: 560,
    width: '100%',
  }
  const wrap: React.CSSProperties = {
    minHeight: '100vh',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: '2rem',
    background: 'var(--bg-base)',
    fontFamily: 'var(--font-sans)',
  }

  if (phase === 'loading' || authLoading) {
    return <div style={wrap}><div style={card}>Loading…</div></div>
  }

  if (phase === 'error') {
    return (
      <div style={wrap}>
        <div style={card}>
          <h2 style={{ marginTop: 0 }}>Cannot load project</h2>
          <p style={{ color: 'var(--fg-muted)' }}>{error}</p>
          <a href="/portal" className="button">Back to Portal</a>
        </div>
      </div>
    )
  }

  if (phase === 'already-allowed') {
    return (
      <div style={wrap}>
        <div style={card}>
          <h2 style={{ marginTop: 0 }}>You already have access</h2>
          <p style={{ color: 'var(--fg-muted)' }}>
            {project?.name} is already accessible from your account.
            Try opening it again — if it still fails, ask the owner to verify.
          </p>
          <a href="/portal" className="button">Back to Portal</a>
        </div>
      </div>
    )
  }

  if (phase === 'submitted') {
    return (
      <div style={wrap}>
        <div style={card}>
          <h2 style={{ marginTop: 0 }}>Request submitted</h2>
          <p style={{ color: 'var(--fg-muted)', lineHeight: 1.5 }}>
            We've notified the project owner. You'll be able to reach
            <strong> {project?.name}</strong> once they approve your request.
          </p>
          {submitted?.reason && (
            <p style={{ color: 'var(--fg-muted)', fontSize: '0.875rem' }}>
              Reason on file: <em>{submitted.reason}</em>
            </p>
          )}
          <a href="/portal" className="button">Back to Portal</a>
        </div>
      </div>
    )
  }

  return (
    <div style={wrap}>
      <div style={card}>
        <h2 style={{ marginTop: 0 }}>Request access</h2>
        <p style={{ color: 'var(--fg-muted)', lineHeight: 1.5 }}>
          {project?.name ? <strong>{project.name}</strong> : 'This project'} is private.
          Send the owner a quick note explaining why you need access — they'll
          decide and you'll be let in once approved.
        </p>
        <label className="form-label" style={{ display: 'block', marginTop: '1rem' }}>
          Reason (optional)
        </label>
        <textarea
          value={reason}
          onChange={e => setReason(e.target.value)}
          rows={4}
          maxLength={1000}
          placeholder="What do you need this for?"
          style={{ width: '100%', resize: 'vertical', padding: '0.5rem', border: '1px solid var(--border)', borderRadius: 4, background: 'var(--bg-input)', color: 'var(--fg-base)' }}
        />
        {error && <p style={{ color: 'var(--accent-coral)', marginTop: '0.5rem' }}>{error}</p>}
        <div style={{ display: 'flex', gap: '0.5rem', marginTop: '1rem' }}>
          <button className="button button-primary" onClick={submit} disabled={busy}>
            {busy ? 'Submitting…' : 'Send request'}
          </button>
          <a href="/portal" className="button">Cancel</a>
        </div>
        <p style={{ color: 'var(--fg-muted)', fontSize: '0.75rem', marginTop: '1rem' }}>
          Signed in as {user?.email}
        </p>
      </div>
    </div>
  )
}
