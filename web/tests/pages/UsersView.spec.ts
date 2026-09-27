import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
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

// The wrapper is mounted detached (document does not contain the page), while
// the modal content is teleported to document.body: page buttons are found via
// the wrapper, modal controls via document.body.
const bodyButtons = () => Array.from(document.querySelectorAll('button'))
const bodyInputs = () => Array.from(document.querySelectorAll('input'))

async function clickWrapperButton(wrapper: any, label: string) {
  const btn = wrapper.findAll('button').find((b: any) => b.text().trim() === label)
  if (!btn) throw new Error(`button ${label} not found in wrapper`)
  await btn.trigger('click')
  await flushPromises()
}

async function clickBodyButton(label: string) {
  const btn = bodyButtons().find(b => b.textContent?.trim() === label)
  if (!btn) throw new Error(`button ${label} not found in body`)
  btn.click()
  await flushPromises()
}

function inputByPlaceholder(placeholder: string) {
  return bodyInputs().find(i => i.getAttribute('placeholder') === placeholder)
}

function setInputValue(input: HTMLInputElement, value: string) {
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('UsersView', () => {
  let wrapper: any

  beforeEach(() => {
    mockListUsers.mockReset()
    mockListRoles.mockReset()
    mockCreateUser.mockReset()
    mockUpdateUser.mockReset()
    mockDeleteUser.mockReset()
    mockListUsers.mockResolvedValue([])
    mockListRoles.mockResolvedValue([])
    mockCreateUser.mockResolvedValue({})
    mockUpdateUser.mockResolvedValue({})
    mockDeleteUser.mockResolvedValue(undefined)
  })

  afterEach(() => {
    wrapper?.unmount?.()
    wrapper = null
  })

  it('loads users on mount', async () => {
    mockListUsers.mockResolvedValue([
      { id: 'u1', username: 'alice', roles: ['admin'], channels: [], oidc_subjects: ['sub-1'] },
    ])
    wrapper = await mountSuspended(UsersView)
    await flushPromises()

    expect(mockListUsers).toHaveBeenCalled()
    expect(wrapper.text()).toContain('alice')
  })

  it('edit modal shows the OIDC Subjects and saves them back', async () => {
    mockListUsers.mockResolvedValue([
      { id: 'u1', username: 'alice', roles: [], channels: [], oidc_subjects: ['sub-a', 'sub-b'] },
    ])
    wrapper = await mountSuspended(UsersView)
    await flushPromises()

    await clickWrapperButton(wrapper, 'Edit')

    const subjectsInput = inputByPlaceholder('comma-separated sub values')
    expect(subjectsInput).toBeDefined()
    expect((subjectsInput as HTMLInputElement).value).toBe('sub-a, sub-b')

    setInputValue(subjectsInput as HTMLInputElement, ' sub-a, sub-c ')
    await flushPromises()
    await clickBodyButton('Save')
    await flushPromises()

    expect(mockUpdateUser).toHaveBeenCalledWith('u1', expect.objectContaining({
      oidc_subjects: ['sub-a', 'sub-c'],
    }))
  })

  it('create modal sends parsed oidc_subjects', async () => {
    wrapper = await mountSuspended(UsersView)
    await flushPromises()

    await clickWrapperButton(wrapper, 'New User')

    const usernameInput = bodyInputs().find(i => i.getAttribute('placeholder') === null)
    expect(usernameInput).toBeDefined()
    setInputValue(usernameInput as HTMLInputElement, 'bob')
    const subjectsInput = inputByPlaceholder('comma-separated sub values')
    expect(subjectsInput).toBeDefined()
    setInputValue(subjectsInput as HTMLInputElement, 'one, two')
    await flushPromises()

    await clickBodyButton('Save')
    await flushPromises()

    expect(mockCreateUser).toHaveBeenCalledWith(expect.objectContaining({
      username: 'bob',
      oidc_subjects: ['one', 'two'],
    }))
  })

  it('empty oidc subject input serializes to an empty array, never null', async () => {
    mockListUsers.mockResolvedValue([
      { id: 'u1', username: 'alice', roles: [], channels: [] },
    ])
    wrapper = await mountSuspended(UsersView)
    await flushPromises()

    await clickWrapperButton(wrapper, 'Edit')
    const subjectsInput = inputByPlaceholder('comma-separated sub values') as HTMLInputElement
    expect(subjectsInput.value).toBe('')

    await clickBodyButton('Save')
    await flushPromises()

    expect(mockUpdateUser).toHaveBeenCalledWith('u1', expect.objectContaining({
      oidc_subjects: [],
    }))
  })
})
