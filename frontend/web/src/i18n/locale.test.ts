// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createWebI18n } from './index'
import {
  bindDocumentLang,
  initialLocale,
  resolveLocale,
  saveLocale,
  savedLocale,
} from './locale'

afterEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('lang')
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function blockedStorage(blocked: 'read' | 'write') {
  vi.stubGlobal('localStorage', {
    clear: () => {},
    getItem: () => {
      if (blocked === 'read') throw new Error('blocked')
      return null
    },
    setItem: () => {
      if (blocked === 'write') throw new Error('blocked')
    },
  })
}

describe('resolveLocale', () => {
  it('prefers a saved locale over the browser languages', () => {
    expect(resolveLocale('de', ['en-US'])).toBe('de')
  })

  it('ignores an unsupported saved value', () => {
    expect(resolveLocale('fr', ['de-AT'])).toBe('de')
    expect(resolveLocale('DE', [])).toBe('en')
  })

  it('takes the first browser language that is supported', () => {
    expect(resolveLocale(null, ['fr-FR', 'de-AT', 'en'])).toBe('de')
  })

  it('matches the primary subtag without regard to case', () => {
    expect(resolveLocale(null, ['DE'])).toBe('de')
  })

  it('does not match a language whose tag only starts with a supported code', () => {
    expect(resolveLocale(null, ['den', 'en-GB'])).toBe('en')
  })

  it('falls back to English', () => {
    expect(resolveLocale(null, [])).toBe('en')
    expect(resolveLocale(null, ['fr-FR'])).toBe('en')
  })
})

describe('locale storage', () => {
  it('saves and reads the choice under flowseer.locale', () => {
    expect(saveLocale('de')).toBe(true)
    expect(localStorage.getItem('flowseer.locale')).toBe('de')
    expect(savedLocale()).toBe('de')
  })

  it('reads storage that throws as nothing saved and starts in the browser language', () => {
    blockedStorage('read')
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['de-AT'])

    expect(savedLocale()).toBeNull()
    expect(initialLocale()).toBe('de')
  })

  it('reports a write that throws', () => {
    blockedStorage('write')

    expect(saveLocale('de')).toBe(false)
  })

  it('starts from a saved locale before the browser language', () => {
    localStorage.setItem('flowseer.locale', 'en')
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['de-AT'])

    expect(initialLocale()).toBe('en')
  })
})

describe('bindDocumentLang', () => {
  it('sets lang at start and after a Composer change', async () => {
    const i18n = createWebI18n('de')
    const stop = bindDocumentLang(i18n.global)

    expect(document.documentElement.lang).toBe('de')

    i18n.global.locale.value = 'en'
    await nextTick()
    expect(document.documentElement.lang).toBe('en')

    stop()
    i18n.global.locale.value = 'de'
    await nextTick()
    expect(document.documentElement.lang).toBe('en')
  })
})
