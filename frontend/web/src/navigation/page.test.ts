import { describe, expect, it } from 'vitest'
import { isPagePath } from './page'

describe('isPagePath', () => {
  it.each([
    '/dashboard',
    '/devices',
    '/clients',
    '/sites',
    '/topology',
    '/devices/device-1',
  ])('accepts %s', (path) => {
    expect(isPagePath(path)).toBe(true)
  })

  it.each([
    '//example.org',
    '/devices/a/b',
    '/devices/a?b',
    '/devices/',
    '/settings',
    '',
  ])('rejects %s', (path) => {
    expect(isPagePath(path)).toBe(false)
  })
})
