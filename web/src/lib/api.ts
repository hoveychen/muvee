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
  },

  users: {
    list: () => request<import('./types').User[]>('/api/users'),
    setRole: (id: string, role: string) => request(`/api/users/${id}/role`, { method: 'PUT', body: JSON.stringify({ role }) }),
  },
}
