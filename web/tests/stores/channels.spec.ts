import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useChannelsStore } from '~/stores/channels'

const mockList = vi.fn()
const mockCreate = vi.fn()
const mockUpdate = vi.fn()
const mockDelete = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    listChannels: (...args: any[]) => mockList(...args),
    createChannel: (...args: any[]) => mockCreate(...args),
    updateChannel: (...args: any[]) => mockUpdate(...args),
    deleteChannel: (...args: any[]) => mockDelete(...args),
  },
}))

describe('useChannelsStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mockList.mockReset()
    mockCreate.mockReset()
    mockUpdate.mockReset()
    mockDelete.mockReset()
    mockList.mockResolvedValue([])
  })

  it('fetchChannels populates the list and toggles loading', async () => {
    mockList.mockResolvedValue([{ id: 'a' }, { id: 'b' }])
    const store = useChannelsStore()

    await store.fetchChannels()

    expect(store.channels).toHaveLength(2)
    expect(store.loading).toBe(false)
  })

  it('createChannel posts then refreshes the list', async () => {
    mockCreate.mockResolvedValue({ id: 'new' })
    const store = useChannelsStore()

    await store.createChannel('new', 'desc')

    expect(mockCreate).toHaveBeenCalledWith({ id: 'new', description: 'desc' })
    expect(mockList).toHaveBeenCalled()
  })

  it('updateChannel updates then refreshes the list', async () => {
    mockUpdate.mockResolvedValue({ id: 'a' })
    const store = useChannelsStore()

    await store.updateChannel('a', { description: 'x' })

    expect(mockUpdate).toHaveBeenCalledWith('a', { description: 'x' })
    expect(mockList).toHaveBeenCalled()
  })

  it('deleteChannel calls the API and refreshes the list', async () => {
    mockDelete.mockResolvedValue(undefined)
    const store = useChannelsStore()

    await store.deleteChannel('test-channel')

    expect(mockDelete).toHaveBeenCalledWith('test-channel')
    expect(mockList).toHaveBeenCalled()
  })

  it('deleteChannel propagates API failures', async () => {
    mockDelete.mockRejectedValue(new Error('delete failed'))
    const store = useChannelsStore()

    await expect(store.deleteChannel('test-channel')).rejects.toThrow('delete failed')
  })
})
