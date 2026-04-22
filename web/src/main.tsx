import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import './index.css'
import './lib/i18n'
import { AuthProvider, useAuth } from './lib/auth'
import { ThemeProvider } from './lib/theme'
import { SettingsProvider } from './lib/settings'
import LoginPage from './pages/Login'
import PortalPage from './pages/Portal'
import Projects from './pages/Projects'
import NewProject from './pages/NewProject'
import ProjectDetail from './pages/ProjectDetail'
import Datasets from './pages/Datasets'
import NewDataset from './pages/NewDataset'
import SecretsPage from './pages/Secrets'
import SettingsTokensPage from './pages/SettingsTokens'
import OnboardPage from './pages/Onboard'
import AdminSettingsPage from './pages/AdminSettings'
import TunnelsPage from './pages/Tunnels'
import Layout, { NodesPage, UsersPage } from './components/Layout'

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh', background: 'var(--bg-base)', fontFamily: 'var(--font-mono)', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
      Loading...
    </div>
  )
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

function RootRedirect() {
  const { user, loading } = useAuth()
  if (loading) return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh', background: 'var(--bg-base)', fontFamily: 'var(--font-mono)', color: 'var(--fg-muted)', fontSize: '0.8rem' }}>
      Loading...
    </div>
  )
  return <Navigate to={user ? '/portal' : '/login'} replace />
}

function App() {
  return (
    <BrowserRouter>
      <ThemeProvider>
      <SettingsProvider>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<RootRedirect />} />
          {/* Onboarding – requires auth but bypasses the normal layout */}
          <Route path="/onboard" element={<RequireAuth><OnboardPage /></RequireAuth>} />
          <Route element={<RequireAuth><Layout /></RequireAuth>}>
            <Route path="/portal" element={<PortalPage />} />
            <Route path="/projects" element={<Projects />} />
            <Route path="/projects/new" element={<NewProject />} />
            <Route path="/projects/:id" element={<ProjectDetail />} />
            <Route path="/datasets" element={<Datasets />} />
            <Route path="/datasets/new" element={<NewDataset />} />
            <Route path="/secrets" element={<SecretsPage />} />
            <Route path="/settings/tokens" element={<SettingsTokensPage />} />
            <Route path="/nodes" element={<NodesPage />} />
            <Route path="/tunnels" element={<TunnelsPage />} />
            <Route path="/users" element={<UsersPage />} />
            <Route path="/admin/settings" element={<AdminSettingsPage />} />
          </Route>
        </Routes>
      </AuthProvider>
      </SettingsProvider>
      </ThemeProvider>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
