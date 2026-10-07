// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  isWorkEmail,
  nextPath,
  sessionOperator,
  sessionRedirect,
  signIn,
  signOut,
} from './session'

afterEach(() => {
  signOut()
  localStorage.clear()
  vi.unstubAllGlobals()
  vi.resetModules()
})

function to(fullPath: string) {
  return { path: fullPath.split('?')[0] ?? '', fullPath }
}

describe('sessionRedirect', () => {
  it('leaves a signed-out visitor on the login page', () => {
    expect(sessionRedirect(to('/login'))).toBeUndefined()
  })

  it('sends a signed-out visitor from a console page to login and keeps the page', () => {
    expect(sessionRedirect(to('/devices/sw-1?tab=ports'))).toEqual({
      path: '/login',
      query: { next: '/devices/sw-1?tab=ports' },
    })
  })

  it('names no next page for the console home', () => {
    expect(sessionRedirect(to('/dashboard'))).toEqual({ path: '/login' })
  })

  it('lets a signed-in operator into the console and past the login page', () => {
    signIn('ada@example.com')

    expect(sessionRedirect(to('/topology'))).toBeUndefined()
    expect(sessionRedirect(to('/login'))).toBe('/dashboard')
  })
})

describe('nextPath', () => {
  it('keeps a console page with its query', () => {
    expect(nextPath('/devices/sw-1?tab=ports')).toBe('/devices/sw-1?tab=ports')
    expect(nextPath('/clients')).toBe('/clients')
  })

  it('falls back to the console home for anything else', () => {
    for (const next of [
      undefined,
      ['/clients'],
      '',
      'https://example.com/dashboard',
      '//example.com/dashboard',
      '/login',
      '/unknown',
    ])
      expect(nextPath(next)).toBe('/dashboard')
  })
})

describe('isWorkEmail', () => {
  it('requires HTML email local characters and dotted domain labels', () => {
    for (const value of [
      'ada@example..com',
      'ada@-example.com',
      'ada@example-.com',
      'ada@example.-com',
      'ada@example.com-',
      'ada@.example.com',
      'ada@example.com.',
      'ada@example_com.net',
      'ada@exam!ple.com',
      'ada@example/com.net',
      'ada(hi)@example.com',
      '"ada"@example.com',
      'ada@例.com',
      `ada@${'a'.repeat(64)}.com`,
      'ada@example.com\n',
      'ada@example.com\r',
      'ada@example.com\r\n',
      'ada@example',
      'ada@',
      '@example.com',
      'ada@@example.com',
    ])
      expect(isWorkEmail(value), value).toBe(false)
    for (const value of [
      'ada+tag@example.com',
      'ADA@EXAMPLE.COM',
      'ada@a.b',
      'ada@ex-ample.co.uk',
      `ada@${'a'.repeat(63)}.com`,
      '.ada..@example.com',
      "a.!#$%&'*+/=?^_`{|}~-@example.com",
    ])
      expect(isWorkEmail(value), value).toBe(true)
  })

  it('accepts an address with a domain and rejects the rest', () => {
    expect(isWorkEmail('ada@example.com')).toBe(true)
    expect(isWorkEmail('ada')).toBe(false)
    expect(isWorkEmail('ada@example')).toBe(false)
    expect(isWorkEmail('ada lovelace@example.com')).toBe(false)
  })
})

describe('session storage', () => {
  it('stays signed out and redirects the console when storage reads throw', async () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {},
      clear: () => {},
    })
    vi.resetModules()
    const fresh = await import('./session')
    expect(fresh.sessionOperator.value).toBeNull()
    expect(fresh.sessionRedirect(to('/devices'))).toEqual({
      path: '/login',
      query: { next: '/devices' },
    })
  })

  it('restores the operator a reload finds in storage', async () => {
    signIn('ada@example.com')
    vi.resetModules()

    const reloaded = await import('./session')

    expect(reloaded.sessionOperator.value).toBe('ada@example.com')
  })

  it('forgets the operator on sign-out', async () => {
    signIn('ada@example.com')
    signOut()
    vi.resetModules()

    const reloaded = await import('./session')

    expect(sessionOperator.value).toBeNull()
    expect(reloaded.sessionOperator.value).toBeNull()
  })

  it('keeps the session for the page when storage refuses the write', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => null,
      setItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {
        throw new Error('blocked')
      },
      clear: () => {},
    })

    signIn('ada@example.com')

    expect(sessionOperator.value).toBe('ada@example.com')
    expect(sessionRedirect(to('/clients'))).toBeUndefined()
  })
})
