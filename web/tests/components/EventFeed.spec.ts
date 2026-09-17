import { describe, it, expect } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import EventFeed from '~/components/EventFeed.vue'

const baseProps = {
  channel: 'my-channel',
  events: [] as any[],
  connected: false,
  connecting: false,
}

describe('EventFeed', () => {
  it('shows the Connected badge when connected', async () => {
    const wrapper = await mountSuspended(EventFeed, {
      props: { ...baseProps, connected: true },
    })

    expect(wrapper.text()).toContain('Connected')
  })

  it('shows the Connecting badge while connecting', async () => {
    const wrapper = await mountSuspended(EventFeed, {
      props: { ...baseProps, connecting: true },
    })

    expect(wrapper.text()).toContain('Connecting...')
  })

  it('shows the Disconnected badge by default', async () => {
    const wrapper = await mountSuspended(EventFeed, { props: baseProps })

    expect(wrapper.text()).toContain('Disconnected')
  })

  it('renders the empty state when there are no events', async () => {
    const wrapper = await mountSuspended(EventFeed, { props: baseProps })

    expect(wrapper.text()).toContain('No events yet')
  })

  it('renders events and the E2E encrypted badge', async () => {
    const wrapper = await mountSuspended(EventFeed, {
      props: {
        ...baseProps,
        connected: true,
        encryptionMode: 'e2e',
        events: [{ id: 1, data: { a: 1 }, timestamp: '2024-01-01T00:00:00Z', encrypted: true }],
      },
    })

    expect(wrapper.text()).toContain('#1')
    expect(wrapper.text()).toContain('E2E Encrypted')
  })

  it('emits replay with the event id', async () => {
    const wrapper = await mountSuspended(EventFeed, {
      props: {
        ...baseProps,
        connected: true,
        events: [{ id: 7, data: { a: 1 }, timestamp: '2024-01-01T00:00:00Z', event_id: 'abcdef123456' }],
      },
    })

    const replay = wrapper.findAll('button').find(b => b.text().includes('Replay'))
    expect(replay).toBeTruthy()
    await replay!.trigger('click')

    expect(wrapper.emitted('replay')?.[0]).toEqual(['abcdef123456'])
  })

  it('formats the message TTL', async () => {
    const wrapper = await mountSuspended(EventFeed, {
      props: { ...baseProps, messageTTL: 3600 },
    })

    expect(wrapper.text()).toContain('Messages retained for 1h')
  })
})
