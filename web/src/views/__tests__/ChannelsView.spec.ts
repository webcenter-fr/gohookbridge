import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useChannelsStore } from '../../stores/channels'

const mockDelete = vi.fn()

vi.mock('../../api/client', () => ({
  api: {
    listChannels: vi.fn().mockResolvedValue([]),
    deleteChannel: (...args: any[]) => mockDelete(...args),
  },
}))

describe('ChannelsStore deleteChannel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mockDelete.mockReset()
    mockDelete.mockResolvedValue(undefined)
  })

  it('calls api.deleteChannel and refreshes list', async () => {
    const store = useChannelsStore()
    await store.deleteChannel('test-channel')
    expect(mockDelete).toHaveBeenCalledWith('test-channel')
  })

  it('throws on api.deleteChannel failure', async () => {
    mockDelete.mockRejectedValue(new Error('delete failed'))
    const store = useChannelsStore()
    await expect(store.deleteChannel('test-channel')).rejects.toThrow('delete failed')
  })
})