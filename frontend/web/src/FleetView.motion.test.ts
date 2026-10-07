// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { mountInFrame } from './navigation/frameTesting'
import { FRAME_MOVE_SECONDS } from './navigation/frame'
import { UiAppRoot } from './ui'
import { UiMotionConfig } from './ui/motion'
import AppFrame from './navigation/AppFrame.vue'
import { createWebI18n } from './i18n'
import { createAiRegistry } from './ai'
import { aiRegistryKey } from './ui/ai/context'

let dispose = () => {}
let preference: (EventTarget & { matches: boolean }) | undefined
let minWidthMatched = true
const mediaQueries = new Map<string, EventTarget & { matches: boolean }>()

beforeEach(() => {
  minWidthMatched = true
  mediaQueries.clear()
  vi.stubGlobal('matchMedia', (query: string) => {
    const existing = mediaQueries.get(query)
    if (existing) return existing
    const media = Object.assign(new EventTarget(), {
      matches: minWidthMatched && query.includes('min-width'),
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
    }) as EventTarget & { matches: boolean }
    mediaQueries.set(query, media)
    if (query === '(prefers-reduced-motion: reduce)') preference = media
    return media
  })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const element = this instanceof HTMLElement ? this : undefined
      // The nav measures its own class before Vue patches it after the shell update.
      const collapsed =
        element?.tagName === 'NAV'
          ? element.classList.contains('max-[800px]:!hidden')
          : element?.closest('.shell')?.classList.contains('sidebar-collapsed')
      const movesWithSidebar = element?.matches(
        '#frame-page, #frame-topbar, .sidebar nav',
      )
      const width = collapsed ? 64 : 204
      const left = movesWithSidebar ? width : 0
      const top = element?.tagName === 'NAV' && !collapsed ? 28 : 0
      const navLink = element?.closest('nav a')
      if (navLink) {
        const navItems = [
          'dashboard',
          'devices',
          'topology',
          'clients',
          'sites',
        ]
        const href = navLink.getAttribute('href') ?? ''
        const index = navItems.findIndex((item) => href.includes(item))
        const top = (index >= 0 ? index : 0) * 44
        const height = 40
        return {
          bottom: top + height,
          height,
          left: 0,
          right: width,
          top,
          width,
          x: 0,
          y: top,
          toJSON: () => ({}),
        } as DOMRect
      }
      return {
        bottom: top + 64,
        height: 64,
        left,
        right: left + width,
        top,
        width,
        x: left,
        y: top,
        toJSON: () => ({}),
      } as DOMRect
    },
  )
})

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function wait(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

// motion's frame loop timestamps every frame from performance.now, so a
// controlled clock makes layout animation progress independent of host speed.
let motionClock = 0

function installMotionClock() {
  motionClock = 0
  vi.spyOn(performance, 'now').mockImplementation(() => motionClock)
}

async function advanceMotion(milliseconds: number) {
  motionClock += milliseconds
  // happy-dom implements requestAnimationFrame with setImmediate, so an
  // awaited macrotask turn lets a pending frame-loop batch read the advanced
  // timestamp. A batch runs only while one is scheduled
  // (motion-dom/dist/es/frameloop/batcher.mjs:43-54), and the batch count per
  // turn is not fixed, so the callers below read whatever progress the clock
  // has reached, mid-flight or final.
  for (let turn = 0; turn < 3; turn += 1) {
    await wait(0)
  }
}

function keyframeEffect(animation: Animation) {
  if (!(animation.effect instanceof KeyframeEffect))
    throw new Error('Expected a native KeyframeEffect')
  return animation.effect
}

function opacityAnimations(element: HTMLElement) {
  return element.getAnimations().filter((animation) => {
    const effect = animation.effect
    return (
      effect instanceof KeyframeEffect &&
      effect.getKeyframes().some((keyframe) => keyframe.opacity !== undefined)
    )
  })
}

async function finishAnimations(...elements: HTMLElement[]) {
  for (const element of elements) {
    for (const animation of [...element.getAnimations()]) animation.finish()
  }
  await Promise.resolve()
  await Promise.resolve()
}

async function mountFleet() {
  const mounted = await mountInFrame(FleetView, '/dashboard', [
    {
      path: '/:view(dashboard|devices|clients|sites|topology)',
      component: FleetView,
    },
    { path: '/devices/:deviceId', component: FleetView },
  ])
  dispose = mounted.dispose
  await nextTick()
  await wait(20)
  return mounted
}

function parseTranslateY(transform: string | undefined): number | null {
  if (!transform || transform === 'none') return null
  const match =
    /translate(?:3d)?\([^,]+,\s*(-?[\d.]+)px/.exec(transform) ??
    /translateY\((-?[\d.]+)px\)/.exec(transform)
  return match ? Number.parseFloat(match[1] ?? '') : null
}

describe('FleetView motion layout', () => {
  it('ends panel, breadcrumb, and nav collapse moves within 160 ms', async () => {
    installMotionClock()
    // UiAppRoot's 140 ms fallback would conceal a missing frame transition.
    const host = document.createElement('div')
    document.body.append(host)
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/dashboard', component: FleetView }],
    })
    const app = createApp({
      render: () =>
        h(UiAppRoot, {}, () =>
          h(UiMotionConfig, { transition: { duration: 0.45 } }, () =>
            h(AppFrame, {}, () => h(FleetView)),
          ),
        ),
    })
    await router.push('/dashboard')
    app.use(createWebI18n('en')).use(router)
    app.provide(aiRegistryKey, createAiRegistry())
    await router.isReady()
    app.mount(host)
    dispose = () => app.unmount()
    await nextTick()
    await advanceMotion(0)
    const selectors = ['#frame-page', '#frame-topbar', '.sidebar nav']
    const elements = selectors.map((selector) => {
      const element = host.querySelector<HTMLElement>(selector)
      if (!element) throw new Error(`Missing moving element: ${selector}`)
      return element
    })
    host.querySelector<HTMLButtonElement>('.sidebar-toggle')?.click()
    await nextTick()
    await advanceMotion(30)
    for (const element of elements)
      expect(
        element.style.transform,
        element.id || element.className,
      ).toContain('translate')
    // 250 ms is past the frame duration and inside motion's 450 ms default.
    await advanceMotion(220)
    for (const element of elements)
      expect(
        element.style.transform,
        element.id || element.className,
      ).not.toContain('translate')
  })

  it('moves the page panel when the sidebar collapses and scales nothing', async () => {
    installMotionClock()
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    toggle?.click()
    await nextTick()
    await advanceMotion(30)

    expect(
      host.querySelector<HTMLElement>('.sidebar')?.style.transform,
    ).not.toContain('scale(')
    const transform =
      host.querySelector<HTMLElement>('#frame-page')?.style.transform ?? ''
    expect(transform).toContain('translate')
    expect(transform).not.toContain('scale(')
    const offset = /translate(?:3d)?\((-?[\d.]+)px/.exec(transform)
    expect(offset).not.toBeNull()
    const distance = Math.abs(Number(offset?.[1]))
    expect(distance).toBeGreaterThan(0)
    expect(distance).toBeLessThan(140)
  })

  it('keeps the switches and their ancestors still while the panel and breadcrumb move', async () => {
    installMotionClock()
    const { host } = await mountFleet()
    const theme = host.querySelector('header.topbar button.theme-switcher')
    const locale = host.querySelector('header.topbar button.locale-switcher')
    const header = host.querySelector('header.topbar')
    const shell = host.querySelector('.main-shell')
    if (!theme || !locale || !header || !shell)
      throw new Error('Missing frame switches')
    const ancestors = new Set<Element>([header, shell])
    for (const button of [theme, locale]) {
      for (
        let parent = button.parentElement;
        parent;
        parent = parent.parentElement
      ) {
        ancestors.add(parent)
        if (parent === header) break
      }
    }
    const transforms: string[] = []
    const observer = new MutationObserver((records) => {
      for (const record of records) {
        if (
          record.target instanceof HTMLElement &&
          ancestors.has(record.target)
        ) {
          transforms.push(
            record.oldValue ?? '',
            record.target.getAttribute('style') ?? '',
          )
        }
      }
    })
    observer.observe(shell, {
      subtree: true,
      attributes: true,
      attributeFilter: ['style'],
      attributeOldValue: true,
    })
    try {
      host.querySelector<HTMLButtonElement>('.sidebar-toggle')?.click()
      await nextTick()
      await advanceMotion(30)
      for (const region of ['#frame-page', '#frame-topbar'])
        expect(
          host.querySelector<HTMLElement>(region)?.style.transform,
        ).toContain('translate')
      await advanceMotion(200)
      expect(host.querySelector('header.topbar button.theme-switcher')).toBe(
        theme,
      )
      expect(host.querySelector('header.topbar button.locale-switcher')).toBe(
        locale,
      )
      for (const ancestor of ancestors)
        expect(ancestor.getAttribute('style') ?? '').not.toMatch(
          /transform\s*:/,
        )
      expect(transforms.every((style) => !/transform\s*:/.test(style))).toBe(
        true,
      )
    } finally {
      observer.disconnect()
    }
  })

  it('stops layout transforms after the user enables reduced motion', async () => {
    installMotionClock()
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')

    if (!preference) throw new Error('Missing reduced-motion media query')
    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()

    toggle?.click()
    await nextTick()
    await advanceMotion(30)

    expect(host.querySelector<HTMLElement>('.sidebar')?.style.transform).toBe(
      '',
    )
    expect(
      host.querySelector<HTMLElement>('#frame-page')?.style.transform,
    ).toBe('')

    await advanceMotion(200)

    expect(host.querySelector<HTMLElement>('.sidebar')?.style.transform).toBe(
      '',
    )
    expect(
      host.querySelector<HTMLElement>('#frame-page')?.style.transform,
    ).toBe('')
  })

  it('animates the nav highlight position across route changes', async () => {
    installMotionClock()
    const { host, router } = await mountFleet()
    await router.push('/devices')
    await nextTick()
    await advanceMotion(30)

    const firstHighlight = host.querySelector<HTMLElement>('.nav-highlight')
    if (!firstHighlight) throw new Error('Missing nav highlight')
    const firstTransform = firstHighlight.style.transform
    const firstOffset = parseTranslateY(firstTransform)

    await advanceMotion(70)

    const secondHighlight = host.querySelector<HTMLElement>('.nav-highlight')
    if (!secondHighlight) throw new Error('Missing nav highlight')
    const secondTransform = secondHighlight.style.transform
    const secondOffset = parseTranslateY(secondTransform)

    expect(firstTransform).toContain('translate')
    expect(secondTransform).toContain('translate')
    if (firstOffset === null || secondOffset === null) {
      throw new Error('Missing translate offset')
    }
    expect(secondOffset).not.toBe(firstOffset)
    expect(Math.abs(secondOffset)).toBeLessThan(Math.abs(firstOffset))

    await advanceMotion(200)

    const highlights = host.querySelectorAll<HTMLElement>('.nav-highlight')
    expect(highlights).toHaveLength(1)
    const finalHighlight = highlights[0]
    if (!finalHighlight) throw new Error('Missing nav highlight')
    const devicesLink = finalHighlight.closest('a')
    expect(devicesLink?.getAttribute('aria-current')).toBe('page')
    expect(devicesLink?.getAttribute('href')).toContain('devices')
    expect(finalHighlight.style.transform).not.toContain('translate')
  })

  it('leaves the nav opacity untouched when expanding at desktop width', async () => {
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    const nav = host.querySelector<HTMLElement>('nav')
    if (!toggle || !nav) throw new Error('Missing navigation controls')
    if (!preference) throw new Error('Missing reduced-motion media query')
    // Reduced motion stops the layout animation from rewriting the nav's inline
    // style, so the width branch is the only thing that can start a fade.
    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()
    nav.style.opacity = '0.42'

    toggle.click()
    await nextTick()
    toggle.click()
    await nextTick()

    expect(nav.getAnimations()).toHaveLength(0)
    expect(nav.style.opacity).toBe('0.42')
    const labels = host.querySelectorAll<HTMLElement>(
      '.product-brand > span, .nav-label, .nav-text, .nav-count',
    )
    expect(labels).toHaveLength(8)
    for (const label of labels) expect(opacityAnimations(label)).toHaveLength(0)
  })

  it('fades labels without fading nav or icons when expanding at desktop width', async () => {
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    const nav = host.querySelector<HTMLElement>('nav')
    if (!toggle || !nav) throw new Error('Missing navigation controls')
    const labels = host.querySelectorAll<HTMLElement>(
      '.product-brand > span, .nav-label, .nav-text, .nav-count',
    )
    const icons = nav.querySelectorAll<SVGElement>('a > svg')
    expect(labels).toHaveLength(8)
    expect(icons).toHaveLength(5)

    toggle.click()
    await nextTick()
    expect(opacityAnimations(nav)).toHaveLength(0)
    for (const label of labels) expect(opacityAnimations(label)).toHaveLength(0)

    toggle.click()
    await nextTick()
    expect(opacityAnimations(nav)).toHaveLength(0)
    for (const icon of icons) expect(icon.getAnimations()).toHaveLength(0)
    for (const label of labels) {
      const animations = opacityAnimations(label)
      expect(animations, label.className).toHaveLength(1)
      const animation = animations[0]
      if (!animation) throw new Error('Missing sidebar label fade')
      expect(animation.playState).toBe('running')
      expect(keyframeEffect(animation).getKeyframes()).toMatchObject([
        { opacity: '0' },
        { opacity: '1' },
      ])
      expect(keyframeEffect(animation).getTiming()).toMatchObject({
        duration: FRAME_MOVE_SECONDS * 1000,
      })
    }
  })

  it('fades the pane scope when the tenant or site changes', async () => {
    const { host, router } = await mountFleet()
    const pane = host.querySelector<HTMLElement>('.main-pane .pane-scroll')
    if (!pane) throw new Error('Missing scoped pane')
    pane.style.opacity = '0.72'

    await router.push({ path: '/devices', query: { tenant: 'aurora-de' } })
    await nextTick()

    const tenantAnimations = pane.getAnimations()
    expect(tenantAnimations).toHaveLength(1)
    const tenantAnimation = tenantAnimations[0]
    if (!tenantAnimation) throw new Error('Missing tenant scope fade')
    expect(tenantAnimation.playState).toBe('running')
    expect(keyframeEffect(tenantAnimation).getKeyframes()).toMatchObject([
      { opacity: '0.85' },
      { opacity: '1' },
    ])
    expect(keyframeEffect(tenantAnimation).getTiming()).toMatchObject({
      duration: 120,
    })

    await finishAnimations(pane)
    expect(pane.getAnimations()).toHaveLength(0)
    expect(pane.style.opacity).toBe('0.72')

    // A site change inside the same tenant moves the site query only.
    await router.push({
      path: '/devices',
      query: { tenant: 'aurora-de', site: 'berlin' },
    })
    await nextTick()

    const siteAnimations = pane.getAnimations()
    expect(siteAnimations).toHaveLength(1)
    const siteAnimation = siteAnimations[0]
    if (!siteAnimation) throw new Error('Missing site scope fade')
    expect(siteAnimation.playState).toBe('running')
    expect(keyframeEffect(siteAnimation).getKeyframes()).toMatchObject([
      { opacity: '0.85' },
      { opacity: '1' },
    ])
    expect(keyframeEffect(siteAnimation).getTiming()).toMatchObject({
      duration: 120,
    })

    await finishAnimations(pane)
    expect(pane.getAnimations()).toHaveLength(0)
    expect(pane.style.opacity).toBe('0.72')
  })

  it('fades nav on expand below desktop width and restores inline opacity', async () => {
    minWidthMatched = false
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    const nav = host.querySelector<HTMLElement>('nav')
    if (!toggle || !nav) throw new Error('Missing mobile navigation controls')
    nav.style.opacity = '0.37'

    toggle.click()
    await nextTick()

    expect(host.querySelector<HTMLElement>('.sidebar')?.style.transform).toBe(
      '',
    )
    expect(
      host.querySelector<HTMLElement>('#frame-page')?.style.transform,
    ).toBe('')

    expect(nav.getAnimations()).toHaveLength(0)
    expect(nav.style.opacity).toBe('0.37')

    toggle.click()
    await nextTick()

    expect(host.querySelector<HTMLElement>('.sidebar')?.style.transform).toBe(
      '',
    )
    expect(
      host.querySelector<HTMLElement>('#frame-page')?.style.transform,
    ).toBe('')

    const expandAnimations = nav.getAnimations()
    expect(expandAnimations).toHaveLength(1)
    const activeAnimation = expandAnimations[0]
    if (!activeAnimation) throw new Error('Missing running expand animation')
    expect(activeAnimation.playState).toBe('running')
    expect(keyframeEffect(activeAnimation).getKeyframes()).toMatchObject([
      { opacity: '0.6' },
      { opacity: '1' },
    ])
    expect(keyframeEffect(activeAnimation).getTiming()).toMatchObject({
      duration: 100,
    })

    await finishAnimations(nav)
    expect(nav.getAnimations()).toHaveLength(0)
    expect(nav.style.opacity).toBe('0.37')
  })

  it('replaces mobile expand fades within one turn', async () => {
    minWidthMatched = false
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    const nav = host.querySelector<HTMLElement>('nav')
    if (!toggle || !nav) throw new Error('Missing mobile navigation controls')
    nav.style.opacity = '0.24'

    toggle.click()
    toggle.click()
    toggle.click()
    toggle.click()
    await nextTick()

    const animations = nav.getAnimations()
    expect(animations).toHaveLength(1)
    const animation = animations[0]
    if (!animation) throw new Error('Missing replacement expand animation')
    expect(animation.playState).toBe('running')
    expect(keyframeEffect(animation).getKeyframes()).toMatchObject([
      { opacity: '0.6' },
      { opacity: '1' },
    ])

    await finishAnimations(nav)
    expect(nav.style.opacity).toBe('0.24')
  })

  it('restores and replays a mobile fade after resize', async () => {
    minWidthMatched = false
    const { host } = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    const nav = host.querySelector<HTMLElement>('nav')
    if (!toggle || !nav) throw new Error('Missing mobile navigation controls')
    nav.style.opacity = '0.18'

    toggle.click()
    toggle.click()
    await nextTick()
    expect(nav.getAnimations()).toHaveLength(1)

    window.dispatchEvent(new Event('resize'))
    expect(nav.getAnimations()).toHaveLength(0)
    expect(nav.style.opacity).toBe('0.18')

    toggle.click()
    toggle.click()
    await nextTick()
    const running = nav.getAnimations()
    expect(running).toHaveLength(1)

    // The native finish writes the final keyframe into the inline style. The
    // resize must cancel and restore the baseline in the same turn, before the
    // composable's completion microtask can run.
    for (const animation of running) animation.finish()
    expect(nav.style.opacity).toBe('1')
    window.dispatchEvent(new Event('resize'))
    expect(nav.getAnimations()).toHaveLength(0)
    expect(nav.style.opacity).toBe('0.18')

    toggle.click()
    toggle.click()
    await nextTick()
    const replayAnimations = nav.getAnimations()
    expect(replayAnimations).toHaveLength(1)
    const replay = replayAnimations[0]
    if (!replay) throw new Error('Missing replay expand animation')
    expect(replay.playState).toBe('running')

    await finishAnimations(nav)
    expect(nav.style.opacity).toBe('0.18')
  })
})
