// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('reduce') || query.includes('min-width'),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
})

afterEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('lang')
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('main entrypoint', () => {
  it('starts in German when browser languages prefer de and storage is empty, and binds document lang', async () => {
    localStorage.clear()
    document.documentElement.removeAttribute('lang')
    const appEl = document.createElement('div')
    appEl.id = 'app'
    document.body.append(appEl)

    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['fr-FR', 'de-AT'])

    await import('./main')
    await new Promise((resolve) => setTimeout(resolve, 100))

    expect(document.documentElement.lang).toBe('de')
    expect(document.body.textContent).toContain('ARBEITSBEREICH')

    const switcher = document.body.querySelector<HTMLButtonElement>(
      'button.locale-switcher',
    )
    if (!switcher) throw new Error('Missing locale switcher button')
    switcher.click()
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(document.documentElement.lang).toBe('en')
  })
})
