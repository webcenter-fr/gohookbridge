import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from '~/stores/auth'

const mockGetMe = vi.fn()
const mockLogin = vi.fn()
const mockLogout = vi.fn()
const mockGetAuthMethods = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    getMe: (...args: any[]) => mockGetMe(...args),
    login: (...args: any[]) => mockLogin(...args),
    logout: (...args: any[]) => mockLogout(...args),
    getAuthMethods: (...args: any[]) => mockGetAuthMethods(...args),
  },
}))

describe('useAuthStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mockGetMe.mockReset()
    mockLogin.mockReset()
    mockLogout.mockReset()
    mockGetAuthMethods.mockReset()
  })

  it('checkSession loads the current user', async () => {
    mockGetMe.mockResolvedValue({ username: 'alice', roles: ['admin'], channels: [], permissions: ['*'] })
    const store = useAuthStore()

    await store.checkSession()

    expect(store.isAuthenticated).toBe(true)
    expect(store.isAdmin).toBe(true)
    expect(store.user?.username).toBe('alice')
  })

  it('checkSession swallows 401 and leaves user null', async () => {
    mockGetMe.mockRejectedValue(new Error('unauthorized'))
    const store = useAuthStore()

    await store.checkSession()

    expect(store.isAuthenticated).toBe(false)
    expect(store.isAdmin).toBe(false)
    expect(store.loaded).toBe(true)
  })

  it('checkSession only calls the API once', async () => {
    mockGetMe.mockResolvedValue({ username: 'alice', roles: [], channels: [], permissions: [] })
    const store = useAuthStore()

    await store.checkSession()
    await store.checkSession()

    expect(mockGetMe).toHaveBeenCalledTimes(1)
  })

  it('login authenticates then reloads the session', async () => {
    mockLogin.mockResolvedValue(undefined)
    mockGetMe.mockResolvedValue({ username: 'bob', roles: [], channels: [], permissions: [] })
    const store = useAuthStore()

    await store.login('bob', 'secret')

    expect(mockLogin).toHaveBeenCalledWith('bob', 'secret')
    expect(store.user?.username).toBe('bob')
  })

  it('logout clears the session', async () => {
    mockGetMe.mockResolvedValue({ username: 'bob', roles: [], channels: [], permissions: [] })
    mockLogout.mockResolvedValue(undefined)
    const store = useAuthStore()
    await store.checkSession()

    await store.logout()

    expect(mockLogout).toHaveBeenCalled()
    expect(store.user).toBeNull()
    expect(store.loaded).toBe(false)
  })

  it('isAdmin is false for non-admin roles', async () => {
    mockGetMe.mockResolvedValue({ username: 'carol', roles: ['viewer'], channels: [], permissions: [] })
    const store = useAuthStore()

    await store.checkSession()

    expect(store.isAdmin).toBe(false)
  })
})
