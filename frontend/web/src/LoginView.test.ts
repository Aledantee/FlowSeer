// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { h, nextTick } from 'vue'
import { RouterView } from 'vue-router'
import LoginView from './LoginView.vue'
import type { WebLocale } from './i18n'
import { i18nWarnings } from './i18n/testing'
import { mountInFrame } from './navigation/frameTesting'
import { sessionOperator, signOut } from './session/session'

let dispose = () => {}

// Reduced motion skips the handover, so sign-in lands at once. One case turns
// motion back on to watch the handover.
let reducedMotion = true

beforeEach(() => {
  reducedMotion = true
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: reducedMotion && query.includes('reduce'),
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
  dispose()
  dispose = () => {}
  signOut()
  localStorage.clear()
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function mountLogin(start = '/login', locale: WebLocale = 'en') {
  const page = { render: () => h('p', 'console') }
  const {
    host,
    router,
    i18n,
    dispose: unmount,
  } = await mountInFrame(
    RouterView,
    start,
    [
      { path: '/', component: page },
      { path: '/login', component: LoginView },
      { path: '/dashboard', component: page },
      { path: '/devices/:deviceId', component: page },
    ],
    locale,
  )
  dispose = unmount
  const input = host.querySelector<HTMLInputElement>('input[type="email"]')
  const form = host.querySelector<HTMLFormElement>(
    '#frame-sidebar form.login-form',
  )
  if (!input || !form) throw new Error('Missing login form')
  return {
    host,
    router,
    i18n,
    input,
    async submit(email: string) {
      input.value = email
      input.dispatchEvent(new Event('input', { bubbles: true }))
      form.dispatchEvent(
        new Event('submit', { bubbles: true, cancelable: true }),
      )
      await nextTick()
      await new Promise((resolve) => setTimeout(resolve, 0))
      await nextTick()
    },
    alert: () => host.querySelector('[role="alert"]')?.textContent?.trim(),
  }
}

describe('LoginView', () => {
  it('labels the email field and titles the page', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { host, input } = await mountLogin()

    expect(host.querySelector('h1')?.textContent?.trim()).toBe(
      'Sign in to FlowSeer',
    )
    expect(host.querySelector(`label[for="${input.id}"]`)?.textContent).toMatch(
      'Work email',
    )
    expect(input.placeholder).toBe('name@company.com')
    expect(document.title).toBe('Sign in · FlowSeer')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('signs in and opens the console home', async () => {
    const { router, submit } = await mountLogin()

    await submit('  ada@example.com ')

    expect(sessionOperator.value).toBe('ada@example.com')
    expect(router.currentRoute.value.fullPath).toBe('/dashboard')
  })

  it('returns to the console page the visitor asked for', async () => {
    const { router, submit } = await mountLogin(
      '/login?next=/devices/sw-1?tab=ports',
    )

    await submit('ada@example.com')

    expect(router.currentRoute.value.fullPath).toBe('/devices/sw-1?tab=ports')
  })

  it('ignores a next page outside the console', async () => {
    const { router, submit } = await mountLogin(
      '/login?next=//example.com/dashboard',
    )

    await submit('ada@example.com')

    expect(router.currentRoute.value.fullPath).toBe('/dashboard')
  })

  it('asks for an email and stays signed out when the field is empty', async () => {
    const { router, input, submit, alert } = await mountLogin()

    await submit('   ')

    expect(alert()).toBe('Enter your work email.')
    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(sessionOperator.value).toBeNull()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('rejects a malformed address and follows a locale switch', async () => {
    const { i18n, submit, alert } = await mountLogin()

    await submit('ada@example')
    expect(alert()).toBe('Enter an email address such as name@company.com.')

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(alert()).toBe('Geben Sie eine E-Mail-Adresse wie name@firma.de ein.')
    expect(sessionOperator.value).toBeNull()
  })

  it('offers the send action only once the address can be sent', async () => {
    const { host, input } = await mountLogin()
    const type = async (value: string) => {
      input.value = value
      input.dispatchEvent(new Event('input', { bubbles: true }))
      await nextTick()
    }
    const send = () =>
      host.querySelector<HTMLButtonElement>('button.login-submit')

    expect(send()).toBeNull()
    await type('ada@example')
    expect(send()).toBeNull()

    await type('ada@example.com')

    expect(send()?.type).toBe('submit')
    expect(send()?.getAttribute('aria-label')).toBe(
      'Continue with single sign-on',
    )
  })

  it('shows the send action signing in before opening the console when motion is allowed', async () => {
    reducedMotion = false
    const { host, router, input, submit } = await mountLogin()

    await submit('ada@example.com')

    const button = host.querySelector<HTMLButtonElement>('button.login-submit')
    expect(button?.getAttribute('aria-busy')).toBe('true')
    expect(button?.disabled).toBe(true)
    expect(host.querySelector('.login-form [role="status"]')?.textContent).toBe(
      'Signing you in',
    )
    expect(input.readOnly).toBe(true)
    expect(sessionOperator.value).toBeNull()
    expect(router.currentRoute.value.path).toBe('/login')

    await new Promise((resolve) => setTimeout(resolve, 600))

    expect(sessionOperator.value).toBe('ada@example.com')
    expect(router.currentRoute.value.fullPath).toBe('/dashboard')
  })

  it('marks the field and keeps the layout when Enter sends a bad address', async () => {
    const { host, input, submit, alert } = await mountLogin()
    const holder = host.querySelector('.login-form [class*="min-h-"]')

    await submit('')

    expect(alert()).toBe('Enter your work email.')
    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(holder?.contains(host.querySelector('[role="alert"]'))).toBe(true)
  })

  it('shows one randomly picked line on the page and follows a locale switch', async () => {
    vi.spyOn(Math, 'random').mockReturnValue(0.2)
    const { host, i18n } = await mountLogin()

    const line = host.querySelector('.login-page .login-line')
    expect(line?.textContent?.trim()).toBe(
      "It's not DNS. There's no way it's DNS. It was DNS.",
    )

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(line?.textContent?.trim()).toBe(
      'Es ist nicht DNS. Es kann nicht DNS sein. Es war DNS.',
    )
  })

  it('uses the console frame: the form in the sidebar, the description on the page', async () => {
    const { host } = await mountLogin()

    expect(host.querySelector('#frame-sidebar form.login-form')).not.toBeNull()
    const page = host.querySelector('#frame-page > main.login-page')
    expect(page?.querySelector('h2')?.textContent?.trim()).toBe(
      'See every network you run in one console.',
    )
    expect(
      [...(page?.querySelectorAll('.login-area') ?? [])].map((area) =>
        area.querySelector('.font-semibold')?.textContent?.trim(),
      ),
    ).toEqual(['Devices', 'Clients', 'Sites', 'Topology'])
  })
})
