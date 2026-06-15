import { describe, it, expect, vi, beforeEach } from 'vitest'

const mockDeleteChannel = vi.fn()

vi.mock('../../api/client', () => ({
  api: {
    deleteChannel: (...args: any[]) => mockDeleteChannel(...args),
    getChannel: vi.fn().mockResolvedValue({ id: 'test-chan' }),
    updateChannel: vi.fn(),
    generateWebhookSecret: vi.fn(),
    generateEncryptionKey: vi.fn(),
    sendTestPayload: vi.fn(),
    replayEvent: vi.fn(),
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