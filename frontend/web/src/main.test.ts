// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

beforeEach(() => {
  vi.resetModules()
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
  Reflect.deleteProperty(document, 'startViewTransition')
  vi.doUnmock('./navigation/frame')
  localStorage.clear()
  document.documentElement.removeAttribute('lang')
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('main entrypoint', () => {
  it.each(['/login', '/', '/topology'])(
    'enters login on initial arrival from %s only',
    async (start) => {
      localStorage.clear()
      history.replaceState(null, '', start)
      const appEl = document.createElement('div')
      appEl.id = 'app'
      document.body.append(appEl)
      await import('./main')
      await vi.waitFor(() => {
        expect(
          document.body
            .querySelector('form.login-form')
            ?.classList.contains('is-entering'),
        ).toBe(true)
      })
      const { signIn, signOut } = await import('./session/session')
      signIn('ada@example.com')
      history.pushState(null, '', '/dashboard')
      window.dispatchEvent(new PopStateEvent('popstate'))
      await vi.waitFor(() =>
        expect(
          document.body.querySelector('#workspace-sidebar'),
        ).not.toBeNull(),
      )
      signOut()
      history.pushState(null, '', '/login')
      window.dispatchEvent(new PopStateEvent('popstate'))
      await vi.waitFor(() => {
        expect(document.body.querySelector('form.login-form')).not.toBeNull()
        expect(document.body.querySelector('.is-entering')).toBeNull()
      })
    },
  )

  it.each(['morph', 'unavailable', 'reduced'])(
    'skips login entrance after console-first logout with %s',
    async (mode) => {
      localStorage.clear()
      localStorage.setItem('flowseer.session', 'ada@example.com')
      localStorage.setItem('flowseer.locale', 'en')
      history.replaceState(null, '', '/dashboard')
      vi.stubGlobal('matchMedia', (query: string) => ({
        matches:
          query.includes('min-width') ||
          (mode === 'reduced' && query.includes('reduce')),
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
        addListener: () => {},
        removeListener: () => {},
      }))
      const pictured = vi.fn((update: () => Promise<void>) => {
        const finished = update()
        return {
          ready: Promise.resolve(),
          finished,
          updateCallbackDone: finished,
          skipTransition() {},
        }
      })
      Object.defineProperty(document, 'startViewTransition', {
        configurable: true,
        value: mode === 'unavailable' ? undefined : pictured,
      })
      const appEl = document.createElement('div')
      appEl.id = 'app'
      document.body.append(appEl)
      await import('./main')
      await vi.waitFor(() =>
        expect(
          document.body.querySelector('button.account-trigger'),
        ).not.toBeNull(),
      )
      document.body
        .querySelector<HTMLButtonElement>('button.account-trigger')
        ?.click()
      await vi.waitFor(() =>
        expect(document.body.querySelector('.account-logout')).not.toBeNull(),
      )
      document.body.querySelector<HTMLElement>('.account-logout')?.click()
      await vi.waitFor(() => {
        expect(location.pathname).toBe('/login')
        expect(document.body.querySelector('form.login-form')).not.toBeNull()
      })
      expect(document.body.querySelector('.is-entering')).toBeNull()
      expect(localStorage.getItem('flowseer.session')).toBeNull()
      expect(pictured).toHaveBeenCalledTimes(mode === 'morph' ? 1 : 0)
    },
  )

  it('starts in German when browser languages prefer de and storage is empty, and binds document lang', async () => {
    localStorage.clear()
    localStorage.setItem('flowseer.session', 'ada@example.com')
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

  it('takes a signed-out visitor from the start address through login into the console', async () => {
    localStorage.clear()
    localStorage.setItem('flowseer.locale', 'en')
    history.replaceState(null, '', '/')
    const appEl = document.createElement('div')
    appEl.id = 'app'
    document.body.append(appEl)
    const settle = () => new Promise((resolve) => setTimeout(resolve, 100))

    await import('./main')
    await settle()

    expect(location.pathname + location.search).toBe('/login')
    expect(document.body.querySelector('#workspace-sidebar')).toBeNull()
    const sidebar = document.body.querySelector('aside.sidebar')
    const mainShell = document.body.querySelector<HTMLElement>('.main-shell')
    expect(sidebar).not.toBeNull()
    expect(mainShell).not.toBeNull()
    const input = document.body.querySelector<HTMLInputElement>(
      'input[type="email"]',
    )
    const form = document.body.querySelector<HTMLFormElement>('form.login-form')
    if (!input || !form) throw new Error('Missing login form')
    input.value = 'ada@example.com'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await settle()

    expect(location.pathname).toBe('/dashboard')
    expect(document.body.querySelector('#workspace-sidebar')).not.toBeNull()
    expect(document.body.querySelector('aside.sidebar')).toBe(sidebar)
    expect(document.body.querySelector('.main-shell')).toBe(mainShell)
    expect(sidebar?.querySelector('a[href^="/devices"]')).not.toBeNull()
    expect(sidebar?.querySelector('form.login-form')).toBeNull()
    expect(mainShell?.style.transform).toBe('')
    expect(localStorage.getItem('flowseer.session')).toBe('ada@example.com')
  })

  it('starts no panel animation when sign-in uses the handover', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query.includes('min-width'),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }))
    let movedCalls = 0
    vi.doMock('./navigation/frame', async (importOriginal) => {
      const actual = await importOriginal<typeof import('./navigation/frame')>()
      return {
        ...actual,
        createFrame: () => {
          const frame = actual.createFrame()
          const moved = frame.moved
          frame.moved = () => {
            movedCalls += 1
            moved()
          }
          return frame
        },
      }
    })
    localStorage.clear()
    localStorage.setItem('flowseer.locale', 'en')
    history.replaceState(null, '', '/login')
    const appEl = document.createElement('div')
    appEl.id = 'app'
    document.body.append(appEl)
    await import('./main')
    await new Promise((resolve) => setTimeout(resolve, 100))
    const input = document.body.querySelector<HTMLInputElement>(
      'input[type="email"]',
    )
    const form = document.body.querySelector<HTMLFormElement>('form.login-form')
    const mainShell = document.body.querySelector<HTMLElement>('.main-shell')
    if (!input || !form || !mainShell) throw new Error('Missing sign-in frame')
    const transforms: string[] = []
    const observer = new MutationObserver(() => {
      if (mainShell.style.transform) transforms.push(mainShell.style.transform)
    })
    observer.observe(mainShell, {
      attributes: true,
      attributeFilter: ['style'],
    })
    input.value = 'ada@example.com'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await new Promise((resolve) => setTimeout(resolve, 600))
    observer.disconnect()
    expect(location.pathname).toBe('/dashboard')
    expect(movedCalls).toBe(0)
    expect(transforms).toEqual([])
  })

  it('sends a signed-out visitor from a console page to login', async () => {
    localStorage.clear()
    history.replaceState(null, '', '/topology')
    const appEl = document.createElement('div')
    appEl.id = 'app'
    document.body.append(appEl)

    await import('./main')
    await new Promise((resolve) => setTimeout(resolve, 100))

    expect(location.pathname + location.search).toBe('/login?next=/topology')
    expect(document.body.querySelector('#workspace-sidebar')).toBeNull()
  })
})
