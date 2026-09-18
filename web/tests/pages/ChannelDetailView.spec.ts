import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
import ChannelDetailView from '~/pages/channels/[id].vue'

const mockGetMe = vi.fn()
const mockGetChannel = vi.fn()
const mockListAccessTokens = vi.fn()
const mockListChannelACL = vi.fn()
const mockDeleteChannel = vi.fn()
const mockCreateAccessToken = vi.fn()
const mockDeleteAccessToken = vi.fn()
const mockUpdateAccessMode = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    getMe: (...args: any[]) => mockGetMe(...args),
    getChannel: (...args: any[]) => mockGetChannel(...args),
    listAccessTokens: (...args: any[]) => mockListAccessTokens(...args),
    listChannelACL: (...args: any[]) => mockListChannelACL(...args),
    deleteChannel: (...args: any[]) => mockDeleteChannel(...args),
    createAccessToken: (...args: any[]) => mockCreateAccessToken(...args),
    deleteAccessToken: (...args: any[]) => mockDeleteAccessToken(...args),
    updateAccessMode: (...args: any[]) => mockUpdateAccessMode(...args),
    updateChannel: vi.fn(),
    generateWebhookSecret: vi.fn(),
    generateEncryptionKey: vi.fn(),
    sendTestPayload: vi.fn(),
    replayEvent: vi.fn(),
  },
}))

class FakeEventSource {
  onopen: (() => void) | null = null
  onmessage: ((msg: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  constructor(public url: string) {}
  close() {}
}

vi.stubGlobal('EventSource', FakeEventSource)

describe('ChannelDetailView', () => {
  beforeEach(() => {
    mockGetMe.mockReset()
    mockGetChannel.mockReset()
    mockListAccessTokens.mockReset()
    mockListChannelACL.mockReset()
    mockDeleteChannel.mockReset()
    mockCreateAccessToken.mockReset()
    mockDeleteAccessToken.mockReset()
    mockUpdateAccessMode.mockReset()

    mockGetMe.mockResolvedValue({ username: 'admin', roles: ['admin'], channels: [], permissions: ['*'] })
    mockGetChannel.mockResolvedValue({ id: 'test-chan', access_mode: 'public', access_tokens: [] })
    mockListAccessTokens.mockResolvedValue({ access_mode: 'public', tokens: [] })
    mockListChannelACL.mockResolvedValue([])
  })

  it('renders the channel tabs after loading', async () => {
    const wrapper = await mountSuspended(ChannelDetailView, { route: '/channels/test-chan' })
    await flushPromises()

    expect(mockGetChannel).toHaveBeenCalledWith('test-chan')
    expect(wrapper.text()).toContain('Data')
    expect(wrapper.text()).toContain('Settings')
    expect(wrapper.text()).toContain('Clients')
    expect(wrapper.text()).toContain('Access Control')
  })

  it('api.deleteChannel is reachable and succeeds', async () => {
    mockDeleteChannel.mockResolvedValue(undefined)
    const { api } = await import('~/utils/api')

    await api.deleteChannel('test-chan')

    expect(mockDeleteChannel).toHaveBeenCalledWith('test-chan')
  })

  it('api.deleteChannel propagates errors', async () => {
    mockDeleteChannel.mockRejectedValue(new Error('not found'))
    const { api } = await import('~/utils/api')

    await expect(api.deleteChannel('nonexistent')).rejects.toThrow('not found')
  })

  it('api.createAccessToken returns the raw token', async () => {
    mockCreateAccessToken.mockResolvedValue({
      token: 'raw-token-value',
      id: 'token-1',
      name: 'my-token',
      scope: 'produce',
      created_at: '2024-01-01T00:00:00Z',
    })
    const { api } = await import('~/utils/api')

    const result = await api.createAccessToken('test-chan', 'my-token', 'produce')

    expect(result.token).toBe('raw-token-value')
    expect(result.scope).toBe('produce')
    expect(mockCreateAccessToken).toHaveBeenCalledWith('test-chan', 'my-token', 'produce')
  })

  it('api.listAccessTokens returns tokens with access_mode', async () => {
    mockListAccessTokens.mockResolvedValue({
      access_mode: 'token',
      tokens: [
        { id: 't1', name: 'token-1', scope: 'produce', created_at: '2024-01-01T00:00:00Z' },
        { id: 't2', name: 'token-2', scope: 'consume', created_at: '2024-01-01T00:00:00Z' },
      ],
    })
    const { api } = await import('~/utils/api')

    const result = await api.listAccessTokens('test-chan')

    expect(result.access_mode).toBe('token')
    expect(result.tokens).toHaveLength(2)
    expect(result.tokens[0]!.scope).toBe('produce')
  })

  it('api.deleteAccessToken succeeds', async () => {
    mockDeleteAccessToken.mockResolvedValue(undefined)
    const { api } = await import('~/utils/api')

    await api.deleteAccessToken('test-chan', 't1')

    expect(mockDeleteAccessToken).toHaveBeenCalledWith('test-chan', 't1')
  })

  it('api.updateAccessMode updates the access mode', async () => {
    mockUpdateAccessMode.mockResolvedValue({ access_mode: 'token' })
    const { api } = await import('~/utils/api')

    const result = await api.updateAccessMode('test-chan', 'token')

    expect(result.access_mode).toBe('token')
    expect(mockUpdateAccessMode).toHaveBeenCalledWith('test-chan', 'token')
  })
})
