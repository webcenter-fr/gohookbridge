import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import { useAuthStore } from '~/stores/auth'
import authMiddleware from '~/middleware/auth.global'
import adminMiddleware from '~/middleware/admin'

const { navigateToMock } = vi.hoisted(() => ({ navigateToMock: vi.fn() }))
mockNuxtImport('navigateTo', () => navigateToMock)

const mockGetMe = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    getMe: (...args: any[]) => mockGetMe(...args),
  },
}))

describe('auth.global middleware', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    navigateToMock.mockReset()
    mockGetMe.mockReset()
  })

  it('redirects unauthenticated users to /login with a redirect query', async () => {
    mockGetMe.mockRejectedValue(new Error('401'))

    await authMiddleware({ meta: {}, fullPath: '/channels' } as any)

    expect(navigateToMock).toHaveBeenCalledWith({ path: '/login', query: { redirect: '/channels' } })
  })

  it('allows public routes without a session', async () => {
    mockGetMe.mockRejectedValue(new Error('401'))

    await authMiddleware({ meta: { public: true }, fullPath: '/login' } as any)

    expect(navigateToMock).not.toHaveBeenCalled()
  })

  it('redirects authenticated users away from guest routes', async () => {
    mockGetMe.mockResolvedValue({ username: 'alice', roles: [], channels: [], permissions: [] })

    await authMiddleware({ meta: { guest: true }, fullPath: '/login' } as any)

    expect(navigateToMock).toHaveBeenCalledWith('/')
  })

  it('allows authenticated users on protected routes', async () => {
    mockGetMe.mockResolvedValue({ username: 'alice', roles: [], channels: [], permissions: [] })

    await authMiddleware({ meta: {}, fullPath: '/channels' } as any)

    expect(navigateToMock).not.toHaveBeenCalled()
  })
})

describe('admin middleware', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    navigateToMock.mockReset()
    mockGetMe.mockReset()
  })

  it('redirects non-admins to /', async () => {
    mockGetMe.mockResolvedValue({ username: 'alice', roles: ['viewer'], channels: [], permissions: [] })
    const auth = useAuthStore()
    await auth.checkSession()

    await adminMiddleware({} as any)

    expect(navigateToMock).toHaveBeenCalledWith('/')
  })

  it('allows admins through', async () => {
    mockGetMe.mockResolvedValue({ username: 'root', roles: ['admin'], channels: [], permissions: ['*'] })
    const auth = useAuthStore()
    await auth.checkSession()

    await adminMiddleware({} as any)

    expect(navigateToMock).not.toHaveBeenCalled()
  })
})
