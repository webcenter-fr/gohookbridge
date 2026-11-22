import { describe, it, expect } from 'vitest'
import { channelIdError, channelDescriptionError, oidcProviderErrors } from '~/utils/validation'

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

describe('oidcProviderErrors', () => {
  const valid = {
    id: 'google',
    name: 'Google',
    client_id: 'client-123',
    client_secret: 'secret',
    issuer_url: 'https://accounts.google.com',
  }

  it('accepts a valid create form', () => {
    expect(oidcProviderErrors(valid, false)).toEqual({
      id: '', name: '', client_id: '', client_secret: '', issuer_url: '',
    })
  })

  it('requires each field on create', () => {
    const errors = oidcProviderErrors(
      { id: '', name: '', client_id: '', client_secret: '', issuer_url: '' },
      false,
    )
    expect(errors.id).toBe('ID required')
    expect(errors.name).toBe('Name required')
    expect(errors.client_id).toBe('Client ID required')
    expect(errors.client_secret).toBe('Client Secret required')
    expect(errors.issuer_url).toBe('Issuer URL required')
  })

  it('does not require client_secret when editing', () => {
    const errors = oidcProviderErrors({ ...valid, client_secret: '' }, true)
    expect(errors.client_secret).toBe('')
  })

  it('rejects a non-https issuer', () => {
    const errors = oidcProviderErrors({ ...valid, issuer_url: 'http://example.com' }, false)
    expect(errors.issuer_url).not.toBe('')
  })

  it('allows a loopback http issuer', () => {
    expect(oidcProviderErrors({ ...valid, issuer_url: 'http://localhost:8080' }, false).issuer_url).toBe('')
    expect(oidcProviderErrors({ ...valid, issuer_url: 'http://127.0.0.1:8080' }, false).issuer_url).toBe('')
  })
})
