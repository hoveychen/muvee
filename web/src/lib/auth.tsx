import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import type { User } from './types'
import { api } from './api'

interface AuthCtx {
  user: User | null
  loading: boolean
  refetch: () => void
}

const AuthContext = createContext<AuthCtx>({ user: null, loading: true, refetch: () => {} })

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  const load = () => {
    api.me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [])
  return <AuthContext.Provider value={{ user, loading, refetch: load }}>{children}</AuthContext.Provider>
}

export const useAuth = () => useContext(AuthContext)
