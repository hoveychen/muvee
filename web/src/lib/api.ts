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
    changeOwner: (id: string, ownerId: string) =>
      request<import('./types').Project>(`/api/projects/${id}/owner`, { method: 'PUT', body: JSON.stringify({ owner_id: ownerId }) }),
    delete: (id: string) => request(`/api/projects/${id}`, { method: 'DELETE' }),
    datasets: (id: string) => request<import('./types').ProjectDataset[]>(`/api/projects/${id}/datasets`),
    setDatasets: (id: string, data: import('./types').ProjectDataset[]) => request<import('./types').ProjectDataset[]>(`/api/projects/${id}/datasets`, { method: 'PUT', body: JSON.stringify(data) }),
    secrets: (id: string) => request<import('./types').ProjectSecretBinding[]>(`/api/projects/${id}/secrets`),
    setSecrets: (id: string, data: Omit<import('./types').ProjectSecretBinding, 'secret_name' | 'secret_type'>[]) =>
      request(`/api/projects/${id}/secrets`, { method: 'PUT', body: JSON.stringify(data) }),
    deploy: (id: string) => request<import('./types').Deployment>(`/api/projects/${id}/deploy`, { method: 'POST' }),
    deployments: (id: string) => request<import('./types').Deployment[]>(`/api/projects/${id}/deployments`),
    metrics: (id: string, limit = 60) => request<import('./types').ContainerMetric[]>(`/api/projects/${id}/metrics?limit=${limit}`),
    traffic: (id: string, limit = 100) => request<import('./types').ProjectTraffic[]>(`/api/projects/${id}/traffic?limit=${limit}`),
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

    // Hosted git repository browser
    repoTree: (id: string, ref?: string, path?: string) => {
      const params = new URLSearchParams()
      if (ref) params.set('ref', ref)
      if (path) params.set('path', path)
      return request<import('./types').RepoTreeEntry[]>(`/api/projects/${id}/repo/tree?${params}`)
    },
    repoBlob: async (id: string, ref?: string, path?: string): Promise<string> => {
      const params = new URLSearchParams()
      if (ref) params.set('ref', ref)
      if (path) params.set('path', path)
      const res = await fetch(`/api/projects/${id}/repo/blob?${params}`, { credentials: 'include' })
      if (!res.ok) throw new Error(res.statusText)
      return res.text()
    },
    repoCommits: (id: string, ref?: string, limit = 20) => {
      const params = new URLSearchParams()
      if (ref) params.set('ref', ref)
      params.set('limit', String(limit))
      return request<import('./types').RepoCommit[]>(`/api/projects/${id}/repo/commits?${params}`)
    },
    repoBranches: (id: string) =>
      request<import('./types').RepoBranch[]>(`/api/projects/${id}/repo/branches`),
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
    list: (projectId: string) => request<import('./types').ApiToken[]>(`/api/projects/${projectId}/tokens`),
    create: (projectId: string, name: string) => request<import('./types').CreatedApiToken>(`/api/projects/${projectId}/tokens`, { method: 'POST', body: JSON.stringify({ name }) }),
    delete: (projectId: string, tokenId: string) => request(`/api/projects/${projectId}/tokens/${tokenId}`, { method: 'DELETE' }),
  },

  meTokens: {
    list: () => request<import('./types').ApiToken[]>('/api/me/tokens'),
    create: (data: { name: string; expires_in?: string }) =>
      request<import('./types').CreatedApiToken>('/api/me/tokens', { method: 'POST', body: JSON.stringify(data) }),
    delete: (tokenId: string) => request(`/api/me/tokens/${tokenId}`, { method: 'DELETE' }),
  },

  secrets: {
    list: () => request<import('./types').Secret[]>('/api/secrets'),
    create: (data: { name: string; type: import('./types').SecretType; value: string }) =>
      request<import('./types').Secret>('/api/secrets', { method: 'POST', body: JSON.stringify(data) }),
    delete: (id: string) => request(`/api/secrets/${id}`, { method: 'DELETE' }),
  },

  nodes: {
    list: () => request<import('./types').Node[]>('/api/nodes'),
    delete: (id: string) => request(`/api/nodes/${id}`, { method: 'DELETE' }),
    metrics: (id: string) => request<import('./types').NodeMetric | null>(`/api/nodes/${id}/metrics`),
  },

  users: {
    list: () => request<import('./types').User[]>('/api/users'),
    setRole: (id: string, role: string) => request(`/api/users/${id}/role`, { method: 'PUT', body: JSON.stringify({ role }) }),
  },

  authorization: {
    status: () => request<import('./types').AuthorizationStatus>('/api/authorization/status'),
    request: () => request<import('./types').AuthorizationRequest>('/api/authorization/request', { method: 'POST' }),
  },

  public: {
    settings: () => fetch('/api/public/settings')
      .then(r => r.ok ? r.json() : Promise.reject(new Error(r.statusText))) as Promise<import('./types').SystemSettings>,
  },

  admin: {
    getSettings: () => request<import('./types').SystemSettings>('/api/admin/settings'),
    updateSettings: (data: Partial<import('./types').SystemSettings>) =>
      request<import('./types').SystemSettings>('/api/admin/settings', { method: 'PUT', body: JSON.stringify(data) }),
    listAuthorizationRequests: () => request<import('./types').AuthorizationRequest[]>('/api/admin/authorization/requests'),
    approveAuthorization: (id: string) => request(`/api/admin/authorization/requests/${id}/approve`, { method: 'PUT' }),
    rejectAuthorization: (id: string) => request(`/api/admin/authorization/requests/${id}/reject`, { method: 'PUT' }),
    health: () => request<import('./types').HealthReport>('/api/admin/health'),
    certs: () => request<import('./types').CertReport>('/api/admin/certs'),
    tunnels: () => request<import('./types').ActiveTunnel[]>('/api/admin/tunnels'),
    tunnelHistory: () => request<import('./types').TunnelHistoryEntry[]>('/api/admin/tunnels/history'),
    uploadBranding: async (type: 'logo' | 'favicon', file: File): Promise<{ url: string }> => {
      const form = new FormData()
      form.append('file', file)
      const res = await fetch(`/api/admin/branding/upload?type=${type}`, {
        method: 'POST',
        body: form,
        credentials: 'include',
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: res.statusText }))
        throw new Error(err.error || res.statusText)
      }
      return res.json()
    },
  },
}
