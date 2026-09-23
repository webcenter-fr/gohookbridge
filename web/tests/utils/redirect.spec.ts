import { describe, it, expect } from 'vitest'
import { safeRedirectPath } from '~/utils/redirect'

describe('safeRedirectPath', () => {
  const cases: Array<[string, string, string]> = [
    ['empty', '', '/'],
    ['relative', '/channels/foo', '/channels/foo'],
    ['query', '/channels/foo?tab=1', '/channels/foo?tab=1'],
    ['absoluteURL', 'https://evil.example.com/phish', '/'],
    ['protocolRelative', '//evil.example.com/phish', '/'],
    ['backslash', '/\\evil.example.com', '/'],
    ['crlf', '/safe\r\nLocation: x', '/'],
  ]

  for (const [name, input, expected] of cases) {
    it(name, () => {
      expect(safeRedirectPath(input)).toBe(expected)
    })
  }
})
