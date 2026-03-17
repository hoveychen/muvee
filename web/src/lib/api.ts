const BASE = ''

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
    credentials: 'include',
  })
  if (!res.ok) {
    if (res.status === 401) {
      window.location.href = '/auth/google/login'
      throw new Error('unauthorized')
    }
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

export const api = {
  me: () => request<import('./types').User>('/api/me'),
  runtime: {
    config: () => request<import('./types').RuntimeConfig>('/api/runtime/config'),
  },

  projects: {
    list: () => request<import('./types').Project[]>('/api/projects'),
    get: (id: string) => request<import('./types').Project>(`/api/projects/${id}`),
    create: (data: Partial<import('./types').Project>) => request<import('./types').Project>('/api/projects', { method: 'POST', body: JSON.stringify(data) }),
    update: (id: string, data: Partial<import('./types').Project>) => request<import('./types').Project>(`/api/projects/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    delete: (id: string) => request(`/api/projects/${id}`, { method: 'DELETE' }),
    datasets: (id: string) => request<import('./types').ProjectDataset[]>(`/api/projects/${id}/datasets`),
    setDatasets: (id: string, data: import('./types').ProjectDataset[]) => request<import('./types').ProjectDataset[]>(`/api/projects/${id}/datasets`, { method: 'PUT', body: JSON.stringify(data) }),
    secrets: (id: string) => request<import('./types').ProjectSecretBinding[]>(`/api/projects/${id}/secrets`),
    setSecrets: (id: string, data: Omit<import('./types').ProjectSecretBinding, 'secret_name' | 'secret_type'>[]) =>
      request(`/api/projects/${id}/secrets`, { method: 'PUT', body: JSON.stringify(data) }),
    deploy: (id: string) => request<import('./types').Deployment>(`/api/projects/${id}/deploy`, { method: 'POST' }),
    deployments: (id: string) => request<import('./types').Deployment[]>(`/api/projects/${id}/deployments`),
    metrics: (id: string, limit = 60) => request<import('./types').ContainerMetric[]>(`/api/projects/${id}/metrics?limit=${limit}`),
    workspaceList: (id: string, path?: string) =>
      request<import('./types').WorkspaceEntry[]>(`/api/projects/${id}/workspace${path ? `?path=${encodeURIComponent(path)}` : ''}`),
    workspaceDownloadUrl: (id: string, path: string) =>
      `/api/projects/${id}/workspace/download?path=${encodeURIComponent(path)}`,
    workspaceUpload: async (id: string, path: string, file: File): Promise<void> => {
      const form = new FormData()
      form.append('file', file)
      const res = await fetch(`/api/projects/${id}/workspace/upload?path=${encodeURIComponent(path)}`, {
        method: 'POST',
        body: form,
        credentials: 'include',
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(err.error || res.statusText)
      }
    },
    workspaceDelete: (id: string, path: string) =>
      request(`/api/projects/${id}/workspace?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),
  },

  datasets: {
    list: () => request<import('./types').Dataset[]>('/api/datasets'),
    get: (id: string) => request<import('./types').Dataset>(`/api/datasets/${id}`),
    create: (data: Partial<import('./types').Dataset>) => request<import('./types').Dataset>('/api/datasets', { method: 'POST', body: JSON.stringify(data) }),
    update: (id: string, data: Partial<import('./types').Dataset>) => request<import('./types').Dataset>(`/api/datasets/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    delete: (id: string) => request(`/api/datasets/${id}`, { method: 'DELETE' }),
    scan: (id: string) => request(`/api/datasets/${id}/scan`, { method: 'POST' }),
    snapshots: (id: string) => request<import('./types').DatasetSnapshot[]>(`/api/datasets/${id}/snapshots`),
    history: (id: string, file?: string) => request<import('./types').FileHistory[]>(`/api/datasets/${id}/history${file ? `?file=${encodeURIComponent(file)}` : ''}`),
  },

  tokens: {
    list: () => request<import('./types').ApiToken[]>('/api/tokens'),
    create: (name: string) => request<import('./types').CreatedApiToken>('/api/tokens', { method: 'POST', body: JSON.stringify({ name }) }),
    delete: (id: string) => request(`/api/tokens/${id}`, { method: 'DELETE' }),
  },

  secrets: {
    list: () => request<import('./types').Secret[]>('/api/secrets'),
    create: (data: { name: string; type: 'password' | 'ssh_key'; value: string }) =>
      request<import('./types').Secret>('/api/secrets', { method: 'POST', body: JSON.stringify(data) }),
    delete: (id: string) => request(`/api/secrets/${id}`, { method: 'DELETE' }),
  },

  nodes: {
    list: () => request<import('./types').Node[]>('/api/nodes'),
    metrics: (id: string) => request<import('./types').NodeMetric | null>(`/api/nodes/${id}/metrics`),
  },

  users: {
    list: () => request<import('./types').User[]>('/api/users'),
    setRole: (id: string, role: string) => request(`/api/users/${id}/role`, { method: 'PUT', body: JSON.stringify({ role }) }),
  },

  public: {
    projects: () => fetch('/api/public/projects')
      .then(r => r.ok ? r.json() : Promise.reject(new Error(r.statusText)))
      .then(data => Array.isArray(data) ? data : []) as Promise<import('./types').PublicProject[]>,
    settings: () => fetch('/api/public/settings')
      .then(r => r.ok ? r.json() : Promise.reject(new Error(r.statusText))) as Promise<import('./types').SystemSettings>,
  },

  admin: {
    getSettings: () => request<import('./types').SystemSettings>('/api/admin/settings'),
    updateSettings: (data: Partial<import('./types').SystemSettings>) =>
      request<import('./types').SystemSettings>('/api/admin/settings', { method: 'PUT', body: JSON.stringify(data) }),
    health: () => request<import('./types').HealthReport>('/api/admin/health'),
  },
}
