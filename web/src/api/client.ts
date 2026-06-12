export interface UserInfo {
  username: string
  roles: string[]
  projects: string[]
  permissions: string[]
}

export interface AuthMethods {
  local_enabled: boolean
  oidc_providers: { id: string; name: string }[]
}

export interface Project {
  id: string
  name: string
  webhook_signatures?: string[]
  allowed_ips?: string[]
  max_body_size?: number
  replay_token?: string
  encryption_enabled?: boolean
  encryption_public_keys?: string[]
}

export interface GlobalConfig {
  server: {
    max_body_size: number
    trust_proxy: boolean
    cors_origin: string
    footer: string
    session_secret: string
  }
  defaults: {
    webhook_signatures: string[]
    allowed_ips: string[]
    replay_token: string
  }
}

export interface User {
  id: string
  username: string
  roles: string[]
  projects: string[]
}

export interface Role {
  name: string
  permissions: string[]
}

export interface Binding {
  user_id: string
  roles: string[]
  projects: string[]
}

export interface OIDCProvider {
  id: string
  name: string
  client_id: string
  client_secret: string
  issuer_url: string
  scopes: string[]
}

class ApiClient {
  private base = '/api'

  private async request<T>(path: string, options?: RequestInit): Promise<T> {
    const res = await fetch(`${this.base}${path}`, {
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      ...options,
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error || `HTTP ${res.status}`)
    }
    if (res.status === 204) return undefined as T
    return res.json()
  }

  async getMe(): Promise<UserInfo> {
    return this.request<UserInfo>('/me')
  }

  async getAuthMethods(): Promise<AuthMethods> {
    return this.request<AuthMethods>('/auth/methods')
  }

  async login(username: string, password: string): Promise<void> {
    const res = await fetch('/api/auth/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: 'Login failed' }))
      throw new Error(body.error || 'Login failed')
    }
  }

  async logout(): Promise<void> {
    await fetch('/logout', { method: 'POST', credentials: 'same-origin' })
  }

  async listProjects(): Promise<Project[]> {
    return this.request<Project[]>('/projects')
  }

  async getProject(id: string): Promise<Project> {
    return this.request<Project>(`/projects/${id}`)
  }

  async createProject(project: Partial<Project>): Promise<Project> {
    return this.request<Project>('/projects', { method: 'POST', body: JSON.stringify(project) })
  }

  async updateProject(id: string, project: Partial<Project>): Promise<Project> {
    return this.request<Project>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(project) })
  }

  async deleteProject(id: string): Promise<void> {
    return this.request<void>(`/projects/${id}`, { method: 'DELETE' })
  }

  async getGlobalConfig(): Promise<GlobalConfig> {
    return this.request<GlobalConfig>('/global')
  }

  async updateGlobalConfig(cfg: Partial<GlobalConfig>): Promise<GlobalConfig> {
    return this.request<GlobalConfig>('/global', { method: 'PUT', body: JSON.stringify(cfg) })
  }

  async listUsers(): Promise<User[]> {
    return this.request<User[]>('/users')
  }

  async getUser(id: string): Promise<User> {
    return this.request<User>(`/users/${id}`)
  }

  async createUser(user: { username: string; password: string; roles?: string[]; projects?: string[] }): Promise<User> {
    return this.request<User>('/users', { method: 'POST', body: JSON.stringify(user) })
  }

  async updateUser(id: string, user: Partial<User & { password: string }>): Promise<User> {
    return this.request<User>(`/users/${id}`, { method: 'PUT', body: JSON.stringify(user) })
  }

  async deleteUser(id: string): Promise<void> {
    return this.request<void>(`/users/${id}`, { method: 'DELETE' })
  }

  async listRoles(): Promise<Role[]> {
    return this.request<Role[]>('/rbac/roles')
  }

  async listBindings(): Promise<Binding[]> {
    return this.request<Binding[]>('/rbac/bindings')
  }

  async updateBinding(userID: string, binding: Partial<Binding>): Promise<Binding> {
    return this.request<Binding>(`/rbac/bindings/${userID}`, { method: 'PUT', body: JSON.stringify(binding) })
  }

  async listOIDCProviders(): Promise<OIDCProvider[]> {
    const methods = await this.getAuthMethods()
    return methods.oidc_providers.map(p => ({ ...p, client_id: '', client_secret: '', issuer_url: '', scopes: [] }))
  }
}

export const api = new ApiClient()