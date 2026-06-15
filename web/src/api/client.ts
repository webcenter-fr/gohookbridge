export interface UserInfo {
  username: string
  roles: string[]
  channels: string[]
  permissions: string[]
}

export interface AuthMethods {
  local_enabled: boolean
  oidc_providers: { id: string; name: string }[]
}

export interface Channel {
  id: string
  name: string
  description?: string
  webhook_secret?: string
  allowed_ips?: string[]
  max_body_size?: number
  message_ttl_seconds?: number
  replay_token?: string
  encryption_mode?: string
  encryption_key?: string
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
    webhook_secret: string
    allowed_ips: string[]
    replay_token: string
    message_ttl_seconds: number
  }
}

export interface User {
  id: string
  username: string
  roles: string[]
  channels: string[]
}

export interface Role {
  name: string
  permissions: string[]
}

export interface Binding {
  user_id: string
  roles: string[]
  channels: string[]
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
    const res = await fetch(`${this.base}/auth/login`, {
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
    await fetch(`${this.base}/auth/logout`, { method: 'POST', credentials: 'same-origin' })
  }

  async listChannels(): Promise<Channel[]> {
    return this.request<Channel[]>('/channels')
  }

  async getChannel(id: string): Promise<Channel> {
    return this.request<Channel>(`/channels/${id}`)
  }

  async createChannel(channel: Partial<Channel>): Promise<Channel> {
    return this.request<Channel>('/channels', { method: 'POST', body: JSON.stringify(channel) })
  }

  async updateChannel(id: string, channel: Partial<Channel>): Promise<Channel> {
    return this.request<Channel>(`/channels/${id}`, { method: 'PUT', body: JSON.stringify(channel) })
  }

  async deleteChannel(id: string): Promise<void> {
    return this.request<void>(`/channels/${id}`, { method: 'DELETE' })
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

  async createUser(user: { username: string; password: string; roles?: string[]; channels?: string[] }): Promise<User> {
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

  async sendTestPayload(channelId: string, payload: Record<string, any>): Promise<void> {
    await this.request<void>(`/send/${channelId}`, { method: 'POST', body: JSON.stringify(payload) })
  }

  async generateWebhookSecret(channelId: string): Promise<{ webhook_secret: string }> {
    return this.request<{ webhook_secret: string }>(`/channels/${channelId}/generate-secret`, { method: 'POST' })
  }

  async generateEncryptionKey(channelId: string, mode: string): Promise<Record<string, any>> {
    return this.request<Record<string, any>>(`/channels/${channelId}/generate-encryption-key`, { method: 'POST', body: JSON.stringify({ mode }) })
  }

  async replayEvent(channelId: string, eventId: string): Promise<void> {
    await this.request<void>(`/channels/${channelId}/events/${eventId}/replay`, { method: 'POST' })
  }
}

export const api = new ApiClient()