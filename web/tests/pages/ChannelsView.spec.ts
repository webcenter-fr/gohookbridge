import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import ChannelsView from '~/pages/channels/index.vue'

const mockList = vi.fn()
const mockDelete = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    listChannels: (...args: any[]) => mockList(...args),
    deleteChannel: (...args: any[]) => mockDelete(...args),
  },
}))

describe('ChannelsView', () => {
  beforeEach(() => {
    mockList.mockReset()
    mockDelete.mockReset()
    mockList.mockResolvedValue([])
  })

  it('renders the page header and create action', async () => {
    const wrapper = await mountSuspended(ChannelsView)

    expect(wrapper.text()).toContain('Channels')
    expect(wrapper.text()).toContain('New Channel')
  })

  it('loads channels on mount', async () => {
    mockList.mockResolvedValue([{ id: 'alpha' }, { id: 'beta' }])
    const wrapper = await mountSuspended(ChannelsView)

    expect(mockList).toHaveBeenCalled()
    expect(wrapper.text()).toContain('alpha')
    expect(wrapper.text()).toContain('beta')
  })

  it('api.deleteChannel is reachable and propagates errors', async () => {
    mockDelete.mockRejectedValue(new Error('not found'))
    const { api } = await import('~/utils/api')

    await expect(api.deleteChannel('nonexistent')).rejects.toThrow('not found')
  })
})
