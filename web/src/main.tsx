import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import './index.css'
import { AuthProvider, useAuth } from './lib/auth'
import LoginPage from './pages/Login'
import Projects from './pages/Projects'
import ProjectDetail from './pages/ProjectDetail'
import Datasets from './pages/Datasets'
import Layout, { NodesPage, UsersPage } from './components/Layout'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh', background: 'var(--bg-base)', fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
      Loading...
    </div>
  )
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<RequireAuth><Layout /></RequireAuth>}>
            <Route index element={<Navigate to="/projects" replace />} />
            <Route path="projects" element={<Projects />} />
            <Route path="projects/new" element={<NewProject />} />
            <Route path="projects/:id" element={<ProjectDetail />} />
            <Route path="datasets" element={<Datasets />} />
            <Route path="nodes" element={<NodesPage />} />
            <Route path="users" element={<UsersPage />} />
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  )
}

function NewProject() {
  const [form, setForm] = useState<Partial<import('./lib/types').Project>>({
    git_branch: 'main',
    dockerfile_path: 'Dockerfile',
    auth_required: false,
    auth_allowed_domains: '',
  })
  const [saving, setSaving] = useState(false)
  const navigate = useNavigate()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      const p = await api.projects.create(form)
      navigate(`/projects/${p.id}`)
    } finally {
      setSaving(false)
    }
  }

  return (
    <RequireAuth>
      <Layout>
        <div className="page-enter max-w-lg">
          <div className="mb-8">
            <p style={{ fontFamily: 'DM Mono', color: 'var(--fg-muted)', fontSize: '0.7rem', letterSpacing: '0.15em' }}>NEW PROJECT</p>
            <h1 style={{ fontFamily: 'Bebas Neue', fontSize: '3rem', color: 'var(--fg-primary)', lineHeight: 1 }}>Create Project</h1>
          </div>
          <form onSubmit={handleSubmit} className="space-y-5">
            {(['name', 'git_url', 'git_branch', 'domain_prefix', 'dockerfile_path'] as const).map(key => (
              <div key={key}>
                <label style={{ fontFamily: 'DM Mono', fontSize: '0.7rem', color: 'var(--fg-muted)', letterSpacing: '0.1em', display: 'block', marginBottom: '0.4rem' }}>
                  {key.replace(/_/g, ' ').toUpperCase()}
                </label>
                <input
                  type="text"
                  value={(form[key] ?? '') as string}
                  onChange={e => setForm({ ...form, [key]: e.target.value })}
                  required={key !== 'dockerfile_path'}
                  className="w-full px-3 py-2 rounded-sm text-sm"
                  style={{ background: 'var(--bg-hover)', border: '1px solid var(--border)', color: 'var(--fg-primary)', fontFamily: 'DM Mono', outline: 'none' }}
                  onFocus={e => { e.target.style.borderColor = 'var(--accent)' }}
                  onBlur={e => { e.target.style.borderColor = 'var(--border)' }}
                />
              </div>
            ))}
            <button
              type="submit"
              disabled={saving}
              className="px-6 py-2.5 rounded-sm text-sm"
              style={{ background: 'var(--accent)', color: '#0f0f0f', fontFamily: 'DM Mono', fontWeight: 500, border: 'none', cursor: saving ? 'not-allowed' : 'pointer' }}
            >
              {saving ? 'Creating...' : 'Create Project'}
            </button>
          </form>
        </div>
      </Layout>
    </RequireAuth>
  )
}

import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from './lib/api'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
