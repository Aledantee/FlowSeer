// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { RouterView } from 'vue-router'
import LoginView from './LoginView.vue'
import type { WebLocale } from './i18n'
import { i18nWarnings } from './i18n/testing'
import { mountInFrame } from './navigation/frameTesting'
import { useFrame } from './navigation/frame'
import { sessionOperator, signOut } from './session/session'

let dispose = () => {}

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
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
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
  it('puts the frame in login mode after a console view', async () => {
    const fromMenu = defineComponent({
      setup() {
        useFrame().sidebar.value = 'menu'
        return () => h(LoginView)
      },
    })
    const mounted = await mountInFrame(fromMenu, '/login', [
      { path: '/login', component: fromMenu },
    ])
    dispose = mounted.dispose
    expect(mounted.host.querySelector('aside.sidebar')?.id).toBe('')
  })

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

  it('skips the login as the development administrator', async () => {
    const { host, router } = await mountLogin('/login?next=/devices/sw-01')
    host.querySelector<HTMLButtonElement>('button.login-dev-skip')?.click()
    await vi.waitFor(() =>
      expect(router.currentRoute.value.fullPath).toBe('/devices/sw-01'),
    )
    expect(sessionOperator.value).toBe('admin@flowseer.dev')
  })

  it('offers no login shortcut outside a development build', async () => {
    vi.stubEnv('DEV', false)
    const { host } = await mountLogin()
    expect(host.querySelector('button.login-dev-skip')).toBeNull()
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

  it('hides send and reports malformed domain labels without signing in', async () => {
    const { host, router, input, submit, alert } = await mountLogin()
    for (const value of [
      'ada@example..com',
      'ada@-example.com',
      'ada@example-.com',
      'ada@example.-com',
      'ada@example.com-',
    ]) {
      await submit(value)
      expect(host.querySelector('button.login-submit'), value).toBeNull()
      expect(alert()).toBe('Enter an email address such as name@company.com.')
      expect(input.getAttribute('aria-invalid')).toBe('true')
      expect(sessionOperator.value).toBeNull()
      expect(localStorage.getItem('flowseer.session')).toBeNull()
      expect(router.currentRoute.value.path).toBe('/login')
    }
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

  it('signs in with motion allowed without advancing a timer or marking the form busy', async () => {
    reducedMotion = false
    const { host, router, input } = await mountLogin()
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const form = host.querySelector<HTMLFormElement>('form.login-form')
    if (!form) throw new Error('Missing login form')
    expect(router.currentRoute.value.path).toBe('/login')
    input.value = 'ada@example.com'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(host.querySelector('button.login-submit')).not.toBeNull()
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await nextTick()
    expect(sessionOperator.value).toBe('ada@example.com')
    expect(form.hasAttribute('aria-busy')).toBe(false)
    await new Promise<void>((resolve) => setImmediate(resolve))
    await nextTick()
    expect(router.currentRoute.value.fullPath).toBe('/dashboard')
  })

  it('animates invalid-submit feedback horizontally when motion is allowed', async () => {
    reducedMotion = false
    const { host, submit, alert } = await mountLogin()
    const field = host.querySelector('.login-form [class*="min-h-"] > div')
    if (!(field instanceof HTMLElement)) throw new Error('Missing email field')
    expect(field.getAnimations()).toHaveLength(0)
    await submit('ada@example')
    expect(alert()).toBe('Enter an email address such as name@company.com.')
    expect(sessionOperator.value).toBeNull()
    const animations = field.getAnimations()
    expect(animations).toHaveLength(1)
    const animation = animations[0]
    if (!(animation?.effect instanceof KeyframeEffect))
      throw new Error('Missing native feedback')
    expect(animation.playState).toBe('running')
    expect(
      animation.effect.getKeyframes().map((keyframe) => keyframe.transform),
    ).toEqual(['translateX(-8px)', 'translateX(0px)'])
    expect(animation.effect.getTiming().duration).toBe(280)
  })

  it('marks the field and keeps the layout when Enter sends a bad address', async () => {
    const { host, input, submit, alert } = await mountLogin()
    const holder = host.querySelector('.login-form [class*="min-h-"]')

    await submit('')

    expect(alert()).toBe('Enter your work email.')
    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(holder?.contains(host.querySelector('[role="alert"]'))).toBe(true)
  })

  it('retranslates the network preview and labels its data as an example', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { host, i18n } = await mountLogin()
    const page = host.querySelector('#frame-page > main.login-page')
    expect(page?.textContent).toContain('Example network')
    expect(page?.textContent).toContain('Gateway')
    expect(page?.textContent).toContain('Traffic at a glance')

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(page?.querySelector('h2')?.textContent?.trim()).toBe(
      'Das Netz sehen. Zusammenhänge verstehen.',
    )
    expect(page?.textContent).toContain('Beispielnetzwerk')
    expect(page?.textContent).toContain('Datenverkehr im Überblick')
    expect(page?.textContent).not.toContain('Example network')
    expect(page?.textContent).not.toContain('Traffic at a glance')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('uses the console frame: the form in the sidebar, the description on the page', async () => {
    const { host } = await mountLogin()

    expect(host.querySelector('#frame-sidebar form.login-form')).not.toBeNull()
    expect(
      host.querySelector('#frame-sidebar button.account-trigger'),
    ).toBeNull()
    const page = host.querySelector('#frame-page > main.login-page')
    expect(page?.querySelector('h2')?.textContent?.trim()).toBe(
      'See the network. Follow the connection.',
    )
    expect(host.querySelectorAll('h1')).toHaveLength(1)
    expect(page?.getAttribute('tabindex')).toBe('0')
    expect(
      [...(page?.querySelectorAll('.login-path h3') ?? [])].map((heading) =>
        heading.textContent?.trim(),
      ),
    ).toEqual(['Gateway', 'Switch', 'Access point'])
    expect(page?.querySelector('figure figcaption')?.textContent).toContain(
      'Example network',
    )
  })
})
