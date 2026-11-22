import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import { flushPromises } from '@vue/test-utils'
import UsersView from '~/pages/admin/users.vue'

const mockListUsers = vi.fn()
const mockListRoles = vi.fn()
const mockCreateUser = vi.fn()
const mockUpdateUser = vi.fn()
const mockDeleteUser = vi.fn()

vi.mock('~/utils/api', () => ({
  api: {
    listUsers: (...args: any[]) => mockListUsers(...args),
    listRoles: (...args: any[]) => mockListRoles(...args),
    createUser: (...args: any[]) => mockCreateUser(...args),
    updateUser: (...args: any[]) => mockUpdateUser(...args),
    deleteUser: (...args: any[]) => mockDeleteUser(...args),
  },
}))

const { toastAddMock } = vi.hoisted(() => ({ toastAddMock: vi.fn() }))
mockNuxtImport('useToast', () => () => ({ add: toastAddMock }))

describe('UsersView', () => {
  beforeEach(() => {
    mockListUsers.mockReset()
    mockListRoles.mockReset()
    mockCreateUser.mockReset()
    mockUpdateUser.mockReset()
    mockDeleteUser.mockReset()
    toastAddMock.mockReset()

    mockListUsers.mockResolvedValue([
      { id: 'u1', username: 'alice', roles: ['admin'], channels: ['*'] },
    ])
    mockListRoles.mockResolvedValue([{ name: 'admin' }])
  })

  afterEach(() => {
    // UModal teleports its content to document.body; clear it so leaked modal
    // markup from one test cannot satisfy another test's query.
    document.body.innerHTML = ''
  })

  it('shows the password field when editing an existing user', async () => {
    const wrapper = await mountSuspended(UsersView)
    await flushPromises()

    // Open the edit modal for the existing user.
    const editButton = wrapper.findAll('button').find(b => b.text() === 'Edit')
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await flushPromises()

    // The password field must be present in the edit modal (regression: it was
    // hidden until form.password was non-empty, making password changes
    // impossible from the UI). UModal teleports its content to document.body.
    const passwordInput = document.body.querySelector('input[type="password"]')
    expect(passwordInput).toBeTruthy()
    expect(passwordInput!.getAttribute('placeholder')).toBe('Leave empty to keep the current password')
  })

  it('shows a required password placeholder when creating a user', async () => {
    const wrapper = await mountSuspended(UsersView)
    await flushPromises()

    const newButton = wrapper.findAll('button').find(b => b.text() === 'New User')
    expect(newButton).toBeTruthy()
    await newButton!.trigger('click')
    await flushPromises()

    const passwordInput = document.body.querySelector('input[type="password"]')
    expect(passwordInput).toBeTruthy()
    expect(passwordInput!.getAttribute('placeholder')).toBe('Required')
  })
})
