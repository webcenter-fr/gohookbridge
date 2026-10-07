import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
import OidcView from '~/pages/admin/oidc.vue'

const mockGetInternalAuth = vi.fn()
const mockSetInternalAuth = vi.fn()
const mockListOIDCProviders = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    getInternalAuth: (...args: any[]) => mockGetInternalAuth(...args),
    setInternalAuth: (...args: any[]) => mockSetInternalAuth(...args),
    listOIDCProviders: (...args: any[]) => mockListOIDCProviders(...args),
    updateOIDCProvider: vi.fn(),
    deleteOIDCProvider: vi.fn(),
  },
}))

const { toastAddMock } = vi.hoisted(() => ({ toastAddMock: vi.fn() }))
mockNuxtImport('useToast', () => () => ({ add: toastAddMock }))

describe('OidcView', () => {
  beforeEach(() => {
    mockGetInternalAuth.mockReset()
    mockSetInternalAuth.mockReset()
    mockListOIDCProviders.mockReset()
    toastAddMock.mockReset()

    mockGetInternalAuth.mockResolvedValue({ enabled: true })
    mockListOIDCProviders.mockResolvedValue([])
  })

  it('renders the internal auth toggle', async () => {
    const wrapper = await mountSuspended(OidcView)
    await flushPromises()

    expect(wrapper.text()).toContain('Username/password (internal) login')
    expect(wrapper.find('[role="switch"]').exists()).toBe(true)
  })

  it('calls setInternalAuth when toggled', async () => {
    mockSetInternalAuth.mockResolvedValue({ enabled: false })

    const wrapper = await mountSuspended(OidcView)
    await flushPromises()

    await wrapper.find('[role="switch"]').trigger('click')
    await flushPromises()

    expect(mockSetInternalAuth).toHaveBeenCalledWith(false)
  })

  it('reverts the switch and shows a toast when setInternalAuth fails', async () => {
    mockSetInternalAuth.mockRejectedValue(new Error('cannot disable internal auth'))

    const wrapper = await mountSuspended(OidcView)
    await flushPromises()

    const switchEl = wrapper.find('[role="switch"]')
    await switchEl.trigger('click')
    await flushPromises()

    expect(mockSetInternalAuth).toHaveBeenCalledWith(false)
    expect(switchEl.attributes('aria-checked')).toBe('true')
    expect(toastAddMock).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'cannot disable internal auth', color: 'error' }),
    )
  })
})
