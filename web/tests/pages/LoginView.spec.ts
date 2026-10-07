import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
import LoginView from '~/pages/login.vue'

const mockGetAuthMethods = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    getAuthMethods: (...args: any[]) => mockGetAuthMethods(...args),
    getMe: vi.fn().mockRejectedValue(new Error('401')),
    login: vi.fn(),
    logout: vi.fn(),
  },
}))

describe('LoginView', () => {
  beforeEach(() => {
    mockGetAuthMethods.mockReset()
  })

  it('renders OIDC buttons and no username form when local auth is disabled', async () => {
    mockGetAuthMethods.mockResolvedValue({
      local_enabled: false,
      oidc_providers: [{ id: 'google', name: 'Google' }],
    })

    const wrapper = await mountSuspended(LoginView)
    await flushPromises()

    expect(wrapper.text()).toContain('Login with Google')
    expect(wrapper.find('input[autocomplete="username"]').exists()).toBe(false)
  })

  it('renders the username form and no OIDC buttons when only local auth is enabled', async () => {
    mockGetAuthMethods.mockResolvedValue({ local_enabled: true, oidc_providers: [] })

    const wrapper = await mountSuspended(LoginView)
    await flushPromises()

    expect(wrapper.find('input[autocomplete="username"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Login with')
  })
})
