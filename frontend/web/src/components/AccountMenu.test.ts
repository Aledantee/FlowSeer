// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { RouterView, createMemoryHistory, createRouter } from 'vue-router'
import AccountMenu from './AccountMenu.vue'
import { createWebI18n } from '../i18n'
import type { WebLocale } from '../i18n'
import { i18nWarnings } from '../i18n/testing'
import { sessionOperator, signIn, signOut } from '../session/session'
import { UiAppRoot } from '../ui'

let dispose = () => {}

afterEach(() => {
  dispose()
  dispose = () => {}
  signOut()
  localStorage.clear()
  document.body.replaceChildren()
  delete document.documentElement.dataset.theme
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

const settle = () => new Promise((resolve) => setTimeout(resolve, 20))

async function mountMenu(signedIn: boolean, locale: WebLocale = 'en') {
  const menu = { render: () => h(AccountMenu, { signedIn }) }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', component: { render: () => h('p', 'login') } },
      { path: '/dashboard', component: menu },
    ],
  })
  await router.push('/dashboard')
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () => h(UiAppRoot, {}, () => h(RouterView)),
  })
  const i18n = createWebI18n(locale)
  app.use(i18n).use(router).mount(host)
  dispose = () => app.unmount()
  await router.isReady()
  await nextTick()
  const trigger = host.querySelector<HTMLButtonElement>(
    'button.account-trigger',
  )
  if (!trigger) throw new Error('Missing account menu trigger')
  async function open() {
    trigger?.click()
    await settle()
  }
  async function choose(item: string) {
    await open()
    const entry = document.body.querySelector<HTMLElement>(`.account-${item}`)
    if (!entry) throw new Error(`Missing account menu item ${item}`)
    entry.click()
    await settle()
  }
  const items = () =>
    [...document.body.querySelectorAll('[role="menuitem"]')].map((item) =>
      item.textContent?.replace(/\s+/g, ' ').trim(),
    )
  const status = (kind: 'theme' | 'locale') =>
    host.querySelector(`.account-${kind}-status`)?.textContent?.trim()
  return { host, router, i18n, trigger, open, choose, items, status }
}

describe('AccountMenu', () => {
  it('logs out and returns to the login page', async () => {
    signIn('ada@example.com')
    const { router, choose } = await mountMenu(true)

    await choose('logout')
    await nextTick()

    expect(sessionOperator.value).toBeNull()
    expect(localStorage.getItem('flowseer.session')).toBeNull()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('offers help, the bug report, and log out only to a signed-in visitor', async () => {
    const signedOut = await mountMenu(false)
    expect(signedOut.trigger.getAttribute('aria-label')).toBe('Preferences')
    await signedOut.open()
    expect(signedOut.items()).toEqual([
      'Switch to dark mode',
      'DESwitch language to Deutsch',
    ])
    dispose()
    document.body.replaceChildren()

    const signedIn = await mountMenu(true)
    expect(signedIn.trigger.getAttribute('aria-label')).toBe('Operator account')
    await signedIn.open()
    expect(signedIn.items()).toEqual([
      'Help',
      'Report a bug',
      'Switch to dark mode',
      'DESwitch language to Deutsch',
      'Log out',
    ])
  })

  it('switches the theme, stores the choice, and announces it', async () => {
    const { choose, status, items, open } = await mountMenu(false)
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(status('theme')).toBe('')

    await choose('theme')

    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(localStorage.getItem('flowseer.theme')).toBe('dark')
    expect(status('theme')).toBe('Dark mode enabled.')
    await open()
    expect(items()[0]).toBe('Switch to light mode')
  })

  it('still switches the theme and says so when storage refuses the choice', async () => {
    vi.stubGlobal('localStorage', {
      clear: () => {},
      removeItem: () => {},
      getItem: () => null,
      setItem: () => {
        throw new Error('blocked')
      },
    })
    const { choose, status } = await mountMenu(false, 'de')

    await choose('theme')

    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(status('theme')).toBe(
      'Darstellung für diese Seite geändert. Der Browser konnte die Einstellung nicht speichern.',
    )
  })

  it('starts from the saved theme', async () => {
    localStorage.setItem('flowseer.theme', 'dark')
    await mountMenu(false)
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('switches the language, stores the choice, and names each language in its own', async () => {
    const warn = vi.spyOn(console, 'warn')
    const { i18n, open, status } = await mountMenu(false)
    await open()
    const name = document.body.querySelector('.account-locale [lang="de"]')
    expect(name?.getAttribute('translate')).toBe('no')
    expect(name?.textContent).toBe('Deutsch')
    document.body.querySelector<HTMLElement>('.account-locale')?.click()
    await settle()

    expect(i18n.global.locale.value).toBe('de')
    expect(localStorage.getItem('flowseer.locale')).toBe('de')
    expect(status('locale')).toBe('Sprache auf Deutsch umgestellt.')
    await open()
    expect(
      document.body.querySelector('.account-locale [lang="en"]')?.textContent,
    ).toBe('English')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('still switches the language and says so when storage refuses the choice', async () => {
    vi.stubGlobal('localStorage', {
      clear: () => {},
      removeItem: () => {},
      getItem: () => null,
      setItem: () => {
        throw new Error('blocked')
      },
    })
    const { i18n, choose, status } = await mountMenu(false)

    await choose('locale')

    expect(i18n.global.locale.value).toBe('de')
    expect(status('locale')).toBe(
      'Sprache für diese Seite geändert. Der Browser konnte die Einstellung nicht speichern.',
    )
  })

  it('opens the help dialog from the menu and returns focus to the trigger on close', async () => {
    const { trigger, choose } = await mountMenu(true)

    await choose('help')

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog?.textContent).toContain('Workspace help')
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    dialog?.querySelector<HTMLButtonElement>('[aria-label="Close"]')?.click()
    await settle()

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
    expect(document.body.style.pointerEvents).not.toBe('none')
  })

  it('opens the bug report with the current page from the menu', async () => {
    const { choose } = await mountMenu(true)

    await choose('report')

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog?.textContent).toContain('Report a bug')
    expect(dialog?.textContent).toContain(`Page: ${window.location.pathname}`)
  })
})
