import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockDeleteChannel = vi.fn()
const mockGetChannel = vi.fn().mockResolvedValue({
  id: 'test-chan',
  access_mode: 'public',
  access_tokens: [],
})
const mockCreateAccessToken = vi.fn()
const mockListAccessTokens = vi.fn().mockResolvedValue({
  access_mode: 'public',
  tokens: [],
})
const mockDeleteAccessToken = vi.fn()
const mockUpdateAccessMode = vi.fn()

vi.mock('../../api/client', () => ({
  api: {
    deleteChannel: (...args: any[]) => mockDeleteChannel(...args),
    getChannel: (...args: any[]) => mockGetChannel(...args),
    updateChannel: vi.fn(),
    generateWebhookSecret: vi.fn(),
    generateEncryptionKey: vi.fn(),
    sendTestPayload: vi.fn(),
    replayEvent: vi.fn(),
    createAccessToken: (...args: any[]) => mockCreateAccessToken(...args),
    listAccessTokens: (...args: any[]) => mockListAccessTokens(...args),
    deleteAccessToken: (...args: any[]) => mockDeleteAccessToken(...args),
    updateAccessMode: (...args: any[]) => mockUpdateAccessMode(...args),
  },
}))

describe('ChannelDetailView delete', () => {
  beforeEach(() => {
    mockDeleteChannel.mockReset()
    mockDeleteChannel.mockResolvedValue(undefined)
  })

  it('api.deleteChannel is reachable and succeeds', async () => {
    const { api } = await import('../../api/client')
    await api.deleteChannel('test-chan')
    expect(mockDeleteChannel).toHaveBeenCalledWith('test-chan')
  })

  it('api.deleteChannel propagates errors', async () => {
    mockDeleteChannel.mockRejectedValue(new Error('not found'))
    const { api } = await import('../../api/client')
    await expect(api.deleteChannel('nonexistent')).rejects.toThrow('not found')
  })
})

describe('Channel token API', () => {
  beforeEach(() => {
    mockCreateAccessToken.mockReset()
    mockListAccessTokens.mockReset()
    mockDeleteAccessToken.mockReset()
    mockUpdateAccessMode.mockReset()
  })

  it('api.createAccessToken returns raw token', async () => {
    mockCreateAccessToken.mockResolvedValue({
      token: 'raw-token-value',
      id: 'token-1',
      name: 'my-token',
      scope: 'produce',
      created_at: '2024-01-01T00:00:00Z',
    })
    const { api } = await import('../../api/client')
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
    const { api } = await import('../../api/client')
    const result = await api.listAccessTokens('test-chan')
    expect(result.access_mode).toBe('token')
    expect(result.tokens).toHaveLength(2)
    expect(result.tokens[0].scope).toBe('produce')
  })

  it('api.deleteAccessToken succeeds', async () => {
    mockDeleteAccessToken.mockResolvedValue(undefined)
    const { api } = await import('../../api/client')
    await api.deleteAccessToken('test-chan', 't1')
    expect(mockDeleteAccessToken).toHaveBeenCalledWith('test-chan', 't1')
  })

  it('api.updateAccessMode updates access mode', async () => {
    mockUpdateAccessMode.mockResolvedValue({ access_mode: 'token' })
    const { api } = await import('../../api/client')
    const result = await api.updateAccessMode('test-chan', 'token')
    expect(result.access_mode).toBe('token')
    expect(mockUpdateAccessMode).toHaveBeenCalledWith('test-chan', 'token')
  })
})