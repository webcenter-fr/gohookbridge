import { describe, it, expect } from 'vitest'
import { channelIdError, channelDescriptionError } from '~/utils/validation'

describe('channelIdError', () => {
  it('requires a value', () => {
    expect(channelIdError('')).toBe('Channel ID required')
  })

  it('rejects invalid characters', () => {
    expect(channelIdError('bad channel!')).toBe('Letters, numbers, hyphens, underscores only')
  })

  it('rejects ids longer than 64 characters', () => {
    expect(channelIdError('a'.repeat(65))).toBe('Max 64 characters')
  })

  it('accepts valid ids', () => {
    expect(channelIdError('my-channel_1')).toBe('')
  })
})

describe('channelDescriptionError', () => {
  it('rejects descriptions longer than 500 characters', () => {
    expect(channelDescriptionError('a'.repeat(501))).toBe('Max 500 characters')
  })

  it('accepts short descriptions', () => {
    expect(channelDescriptionError('short')).toBe('')
  })
})
