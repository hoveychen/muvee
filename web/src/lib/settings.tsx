import { createContext, useContext, useEffect, useState, ReactNode, useCallback } from 'react'
import type { SystemSettings } from './types'
import { api } from './api'

interface SettingsCtx {
  settings: SystemSettings
  loading: boolean
  refetch: () => Promise<void>
}

const DEFAULT_SETTINGS: SystemSettings = {
  onboarded: 'false',
  site_name: '',
  logo_url: '',
  favicon_url: '',
  require_authorization: '',
}

const SettingsContext = createContext<SettingsCtx>({
  settings: DEFAULT_SETTINGS,
  loading: true,
  refetch: () => Promise.resolve(),
})

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<SystemSettings>(DEFAULT_SETTINGS)
  const [loading, setLoading] = useState(true)

  const load = useCallback(() => {
    return api.public.settings()
      .then(s => {
        setSettings(s)
        // Apply branding side-effects
        const title = s.site_name || 'muvee'
        document.title = title
        if (s.favicon_url) {
          let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
          if (!link) {
            link = document.createElement('link')
            link.rel = 'icon'
            document.head.appendChild(link)
          }
          link.href = s.favicon_url
        }
      })
      .catch(() => setSettings(DEFAULT_SETTINGS))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load() }, [load])

  return (
    <SettingsContext.Provider value={{ settings, loading, refetch: load }}>
      {children}
    </SettingsContext.Provider>
  )
}

export const useSettings = () => useContext(SettingsContext)
