// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, Teleport, watch } from 'vue'
import { RouterView } from 'vue-router'
import FleetView from '../FleetView.vue'
import LoginView from '../LoginView.vue'
import { sessionRedirect, signOut } from '../session/session'
import { FRAME_MOVE_SECONDS, useFrame } from './frame'
import { chooseAccountItem, mountInFrame } from './frameTesting'

let dispose = () => {}
let motionClock = 0
let reduced = false
let desktop = true
const regions = ['sidebar', 'topbar', 'topbar-tools', 'page']

beforeEach(() => {
  // Mount tests do not load the utility stylesheet used by the AI wrappers.
  const style = document.createElement('style')
  style.textContent = '.contents { display: contents; }'
  document.body.append(style)
  signOut()
  localStorage.clear()
  motionClock = 0
  reduced = false
  desktop = true
  const queries = new Map<string, EventTarget & { matches: boolean }>()
  vi.stubGlobal('matchMedia', (query: string) => {
    if (!queries.has(query))
      queries.set(
        query,
        Object.assign(new EventTarget(), {
          matches: query.includes('min-width')
            ? desktop
            : query.includes(': reduce') && reduced,
          media: query,
          addListener() {},
          removeListener() {},
        }),
      )
    return queries.get(query)
  })
  vi.spyOn(performance, 'now').mockImplementation(() => motionClock)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const shell = this.closest('.shell')
      const width = shell?.classList.contains('sidebar-login')
        ? 420
        : shell?.classList.contains('sidebar-collapsed')
          ? 64
          : 204
      const left = ['frame-page', 'frame-topbar'].includes(this.id) ? width : 0
      return {
        bottom: 64,
        height: 64,
        left,
        right: left + width,
        top: 0,
        width,
        x: left,
        y: 0,
        toJSON: () => ({}),
      } as DOMRect
    },
  )
})

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  signOut()
  localStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function advanceMotion(milliseconds: number) {
  motionClock += milliseconds
  // Let pending requestAnimationFrame batches sample the controlled clock.
  for (let turn = 0; turn < 3; turn += 1)
    await new Promise((resolve) => setTimeout(resolve, 0))
}

async function mountLogin() {
  let arrived: string[] = []
  const view = defineComponent({
    setup() {
      const frame = useFrame()
      watch(
        frame.sidebar,
        async (mode, previous) => {
          if ((mode === 'login') === (previous === 'login')) return
          await nextTick()
          arrived = regions.filter(
            (region) =>
              document.querySelector(`#frame-${region}`)?.childElementCount,
          )
        },
        { flush: 'sync' },
      )
      return () => h(RouterView)
    },
  })
  const mounted = await mountInFrame(view, '/login', [
    { path: '/login', component: LoginView },
    { path: '/dashboard', component: FleetView },
  ])
  dispose = mounted.dispose
  mounted.router.beforeEach(sessionRedirect)
  await advanceMotion(0)
  return { ...mounted, arrived: () => arrived }
}

async function submit(
  host: HTMLElement,
  router: Awaited<ReturnType<typeof mountLogin>>['router'],
) {
  const input = host.querySelector<HTMLInputElement>('input[type="email"]')
  const form = host.querySelector('form.login-form')
  if (!input || !form) throw new Error('Missing login form')
  input.value = 'ada@example.com'
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await navigate(router, () =>
    form.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    ),
  )
  expect(router.currentRoute.value.path).toBe('/dashboard')
  expect(localStorage.getItem('flowseer.session')).toBe('ada@example.com')
  await nextTick()
}

async function navigate(
  router: Awaited<ReturnType<typeof mountLogin>>['router'],
  action: () => void,
) {
  const navigation = new Promise<void>((resolve) => {
    const remove = router.afterEach(() => {
      remove()
      resolve()
    })
  })
  action()
  await navigation
  await nextTick()
}

function expectFades(host: HTMLElement, filled: string[]) {
  for (const region of filled) {
    const children = host.querySelector(`#frame-${region}`)?.children
    expect(children?.length).toBeGreaterThan(0)
    const targets = [...(children ?? [])]
    if (region === 'page' && host.querySelector('[data-ai-context-layer]')) {
      const wrappers = host.querySelectorAll('#frame-page .contents')
      expect(wrappers).toHaveLength(2)
      for (const wrapper of wrappers) {
        expect(getComputedStyle(wrapper).display).toBe('contents')
        expect(wrapper.getAnimations()).toHaveLength(0)
      }
      const main = host.querySelector('#frame-page main#main')
      if (!main) throw new Error('Missing console content box')
      expect(getComputedStyle(main).display).toBe('block')
      targets.splice(0, targets.length, main)
    }
    expect(host.querySelector('#frame-page')?.getAnimations()).toHaveLength(0)
    for (const child of targets) {
      const fades = child
        .getAnimations()
        .filter(
          (animation) =>
            animation.effect instanceof KeyframeEffect &&
            animation.effect
              .getKeyframes()
              .some((keyframe) => keyframe.opacity !== undefined),
        )
      expect(fades).toHaveLength(1)
      const animation = fades[0]
      if (!(animation?.effect instanceof KeyframeEffect))
        throw new Error('Missing content fade')
      expect(animation.playState).toBe('running')
      expect(animation.effect.getKeyframes()).toMatchObject([
        { opacity: '0' },
        { opacity: '1' },
      ])
      expect(animation.effect.getTiming().duration).toBe(
        FRAME_MOVE_SECONDS * 1000,
      )
    }
  }
}

function translation(host: HTMLElement) {
  const transform =
    host.querySelector<HTMLElement>('#frame-page')?.style.transform ?? ''
  const offset = /translate(?:3d)?\((-?[\d.]+)px/.exec(transform)
  expect(offset).not.toBeNull()
  return Number(offset?.[1])
}

for (const mode of ['menu', 'collapsed'] as const)
  it(`fades the nearest boxes through nested contents wrappers on ${mode} crossings`, async () => {
    let frame: ReturnType<typeof useFrame> | undefined
    const view = defineComponent({
      setup() {
        frame = useFrame()
        return () =>
          regions.map((region) =>
            h(Teleport, { to: `#frame-${region}`, defer: true }, [
              h('div', { class: 'contents' }, [
                h('div', { style: { display: 'contents' } }, [
                  h('section', { 'data-box': '' }, [
                    h('span', { 'data-inside-box': '' }, 'Content'),
                  ]),
                  h('div', { style: { display: 'none' } }, 'Hidden'),
                ]),
                h('span', { 'data-box': '' }, 'Sibling'),
              ]),
            ]),
          )
      },
    })
    const mounted = await mountInFrame(view, '/test', [
      { path: '/test', component: view },
    ])
    dispose = mounted.dispose
    if (!frame) throw new Error('Missing frame context')
    for (const crossing of [mode, 'login'] as const) {
      frame.sidebar.value = crossing
      await nextTick()
      for (const region of regions) {
        const root = mounted.host.querySelector(`#frame-${region}`)
        const boxes = root?.querySelectorAll('[data-box]')
        expect(boxes).toHaveLength(2)
        for (const box of boxes ?? []) {
          const animations = box.getAnimations()
          expect(animations).toHaveLength(1)
          const effect = animations[0]?.effect
          if (!(effect instanceof KeyframeEffect))
            throw new Error('Missing box fade')
          expect(effect.getKeyframes()).toMatchObject([
            { opacity: '0' },
            { opacity: '1' },
          ])
          expect(effect.getTiming().duration).toBe(FRAME_MOVE_SECONDS * 1000)
        }
        const skipped = root?.querySelectorAll('div, [data-inside-box]')
        expect(skipped).toHaveLength(4)
        for (const element of skipped ?? [])
          expect(element.getAnimations()).toHaveLength(0)
      }
      expect(
        mounted.host.querySelector('#frame-page')?.getAnimations(),
      ).toHaveLength(0)
    }
  })

it('morphs directly into populated console regions with a 160 ms fade', async () => {
  expect(FRAME_MOVE_SECONDS).toBe(0.16)
  const mounted = await mountLogin()
  await submit(mounted.host, mounted.router)
  expect(mounted.arrived()).toEqual(regions)
  expectFades(mounted.host, regions)
  await advanceMotion(30)
  expect(translation(mounted.host)).toBeGreaterThan(0)
  expect(translation(mounted.host)).toBeLessThan(356)
  await advanceMotion(120)
  expect(translation(mounted.host)).toBeGreaterThan(0)
  expect(translation(mounted.host)).toBeLessThan(356)
  await advanceMotion(200)
  expect(
    mounted.host.querySelector<HTMLElement>('#frame-page')?.style.transform,
  ).not.toContain('translate')
})

for (const mode of ['reduced motion', 'mobile width'] as const)
  it(`fades arriving content without moving the panel under ${mode}`, async () => {
    reduced = mode === 'reduced motion'
    desktop = mode !== 'mobile width'
    const mounted = await mountLogin()
    const page = mounted.host.querySelector<HTMLElement>('#frame-page')
    if (!page) throw new Error('Missing frame panel')
    const styles: string[] = []
    const observer = new MutationObserver((records) => {
      for (const record of records)
        styles.push(record.oldValue ?? '', page.getAttribute('style') ?? '')
    })
    observer.observe(page, {
      attributes: true,
      attributeFilter: ['style'],
      attributeOldValue: true,
    })
    try {
      await submit(mounted.host, mounted.router)
      expect(mounted.arrived()).toEqual(regions)
      expectFades(mounted.host, regions)
      await advanceMotion(30)
      await advanceMotion(200)
      expect(page.style.transform).toBe('')
      expect(styles.every((style) => !/transform\s*:/.test(style))).toBe(true)
    } finally {
      observer.disconnect()
    }
  })

it('reverses the morph on logout and fades the form in the same sidebar', async () => {
  const mounted = await mountLogin()
  const sidebar = mounted.host.querySelector('aside.sidebar')
  await submit(mounted.host, mounted.router)
  await advanceMotion(200)
  mounted.host
    .querySelector<HTMLButtonElement>('button.account-trigger')
    ?.click()
  await nextTick()
  const logout = document.body.querySelector<HTMLElement>('.account-logout')
  if (!logout) throw new Error('Missing logout action')
  await navigate(mounted.router, () => logout.click())
  expect(mounted.router.currentRoute.value.path).toBe('/login')
  expect(localStorage.getItem('flowseer.session')).toBeNull()
  expect(mounted.host.querySelector('aside.sidebar')).toBe(sidebar)
  expect(sidebar?.querySelector('form.login-form')).not.toBeNull()
  expect(mounted.arrived()).toEqual(['sidebar', 'page'])
  expectFades(mounted.host, ['sidebar', 'page'])
  await advanceMotion(30)
  expect(translation(mounted.host)).toBeGreaterThan(-356)
  expect(translation(mounted.host)).toBeLessThan(0)
  await advanceMotion(200)
  expect(
    mounted.host.querySelector<HTMLElement>('#frame-page')?.style.transform,
  ).not.toContain('translate')
})

it('preserves the account control and its theme without transforming its ancestors', async () => {
  const mounted = await mountLogin()
  // The control's root outlives the swap between the signed-out buttons and
  // the account menu.
  const account = mounted.host.querySelector<HTMLElement>(
    'header.topbar .account-control',
  )
  const shell = mounted.host.querySelector<HTMLElement>('.main-shell')
  const header = mounted.host.querySelector('header.topbar')
  if (!account || !shell || !header) throw new Error('Missing account menu')
  const beforeChoice = document.documentElement.dataset.theme
  expect(['dark', 'light']).toContain(beforeChoice)
  await chooseAccountItem('theme', mounted.host)
  const chosen = document.documentElement.dataset.theme
  expect(chosen).toBe(beforeChoice === 'dark' ? 'light' : 'dark')
  const ancestors = new Set<Element>([shell, header])
  for (
    let parent = account.parentElement;
    parent;
    parent = parent.parentElement
  ) {
    ancestors.add(parent)
    if (parent === header) break
  }
  const styles: string[] = []
  const observer = new MutationObserver((records) => {
    for (const record of records)
      if (record.target instanceof HTMLElement && ancestors.has(record.target))
        styles.push(
          record.oldValue ?? '',
          record.target.getAttribute('style') ?? '',
        )
  })
  observer.observe(shell, {
    subtree: true,
    attributes: true,
    attributeFilter: ['style'],
    attributeOldValue: true,
  })
  try {
    await submit(mounted.host, mounted.router)
    await advanceMotion(30)
    expect(translation(mounted.host)).toBeGreaterThan(0)
    expect(document.documentElement.dataset.theme).toBe(chosen)
    await advanceMotion(200)
    // The console opens collapsed, so the toggle expands the sidebar.
    mounted.host.querySelector<HTMLButtonElement>('.sidebar-toggle')?.click()
    await nextTick()
    await advanceMotion(30)
    expect(translation(mounted.host)).toBeLessThan(0)
    await advanceMotion(200)
    signOut()
    await mounted.router.push('/login')
    await nextTick()
    await advanceMotion(30)
    await advanceMotion(200)
    expect(mounted.host.querySelector('.account-control')).toBe(account)
    expect(account.isConnected).toBe(true)
    expect(account.querySelector('button.account-trigger')).toBeNull()
    expect(account.querySelector('button.account-theme')).not.toBeNull()
    expect(document.documentElement.dataset.theme).toBe(chosen)
    for (const ancestor of ancestors)
      expect(ancestor.getAttribute('style') ?? '').not.toMatch(/transform\s*:/)
    expect(styles.every((style) => !/transform\s*:/.test(style))).toBe(true)
  } finally {
    observer.disconnect()
  }
})
