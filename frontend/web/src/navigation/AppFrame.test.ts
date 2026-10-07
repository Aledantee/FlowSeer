// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref, Teleport } from 'vue'
import type { Ref } from 'vue'
import FleetView from '../FleetView.vue'
import LoginView from '../LoginView.vue'
import { useFrame } from './frame'
import { mountInFrame } from './frameTesting'

let dispose = () => {}

beforeEach(() =>
  vi.stubGlobal('matchMedia', () => ({
    matches: true,
    addEventListener() {},
    removeEventListener() {},
  })),
)
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})

for (const mode of ['login', 'menu', 'collapsed'] as const)
  it(`ends the top bar with one theme and language switch in ${mode} mode`, async () => {
    let frame: ReturnType<typeof useFrame> | undefined
    const child = defineComponent({
      setup() {
        frame = useFrame()
        return () => h(mode === 'login' ? LoginView : FleetView)
      },
    })
    const mounted = await mountInFrame(child, '/dashboard', [
      { path: '/dashboard', component: child },
    ])
    dispose = mounted.dispose
    if (!frame) throw new Error('Missing frame context')
    frame.sidebar.value = mode
    await nextTick()
    const header = mounted.host.querySelector('header.topbar')
    expect(header).not.toBeNull()
    expect(header?.querySelectorAll('button.theme-switcher')).toHaveLength(1)
    expect(header?.querySelectorAll('button.locale-switcher')).toHaveLength(1)
    const buttons = [...(header?.querySelectorAll('button') ?? [])]
    const theme = header?.querySelector('button.theme-switcher')
    const locale = header?.querySelector('button.locale-switcher')
    if (mode === 'login') expect(buttons).toEqual([theme, locale])
    else
      expect(buttons.slice(-3)).toEqual([
        header?.querySelector('button.account-trigger'),
        theme,
        locale,
      ])
    expect(mounted.host.querySelector('aside .theme-switcher')).toBeNull()
    expect(mounted.host.querySelector('aside .locale-switcher')).toBeNull()
  })

it('keeps five regions in one frame and clears teleported content on unmount', async () => {
  const visible = ref(true)
  const child = defineComponent({
    setup() {
      const frame = useFrame()
      frame.sidebar.value = 'collapsed'
      return () =>
        visible.value
          ? ['skip', 'sidebar', 'topbar', 'topbar-tools', 'page'].map(
              (region) =>
                h(
                  Teleport,
                  { to: `#frame-${region}`, defer: true },
                  h('span', region),
                ),
            )
          : null
    },
  })
  const mounted = await mountInFrame(child, '/test', [
    { path: '/test', component: child },
  ])
  dispose = mounted.dispose
  for (const region of ['skip', 'sidebar', 'topbar', 'topbar-tools', 'page'])
    expect(
      mounted.host.querySelector(`#frame-${region} span`)?.textContent,
    ).toBe(region)
  expect(
    mounted.host
      .querySelector('.shell')
      ?.classList.contains('sidebar-collapsed'),
  ).toBe(true)
  expect(mounted.host.querySelector('aside')?.id).toBe('workspace-sidebar')
  visible.value = false
  await nextTick()
  expect(mounted.host.querySelector('#frame-sidebar')).not.toBeNull()
  expect(mounted.host.querySelector('#frame-sidebar span')).toBeNull()
})

const pageClasses = [
  'bg-glass-panel',
  'flex',
  'min-h-0',
  'flex-1',
  'flex-col',
  'overflow-hidden',
  'rounded-tl-[18px]',
  'border-t',
  'border-l',
  'border-chrome-border',
  'max-[800px]:rounded-tl-none',
  'max-[800px]:border-l-0',
]

for (const mode of ['login', 'menu', 'collapsed'] as const)
  it(`gives the page region its own box in ${mode} mode`, async () => {
    const child = defineComponent({
      setup() {
        useFrame().sidebar.value = mode
        return () => null
      },
    })
    const mounted = await mountInFrame(child, '/test', [
      { path: '/test', component: child },
    ])
    dispose = mounted.dispose
    await nextTick()
    const page = mounted.host.querySelector('#frame-page')
    for (const name of pageClasses) expect(page?.classList).toContain(name)
    expect(mounted.host.querySelector('.main-notch')).toBeNull()
    expect(mounted.host.querySelector('#frame-topbar')?.textContent).toBe('')
  })

it('paints one empty glass surface before the frame regions', async () => {
  const child = defineComponent({ setup: () => () => null })
  const mounted = await mountInFrame(child, '/test', [
    { path: '/test', component: child },
  ])
  dispose = mounted.dispose
  const shell = mounted.host.querySelector('.shell')
  const glass = shell?.querySelector('.frame-glass')
  expect(glass).not.toBeNull()
  expect(shell?.firstElementChild).toBe(glass)
  expect(glass?.childNodes.length).toBe(0)
  expect(glass?.getAttribute('aria-hidden')).toBe('true')
  expect(shell?.classList).toContain('relative')
  for (const name of [
    'pointer-events-none',
    'absolute',
    'inset-0',
    'bg-glass',
    'backdrop-blur-2xl',
    'backdrop-saturate-150',
  ])
    expect(glass?.classList).toContain(name)
})

it('keeps the sidebar classes constant while the shell carries its mode', async () => {
  let frame: ReturnType<typeof useFrame> | undefined
  const child = defineComponent({
    setup() {
      frame = useFrame()
      frame.sidebar.value = 'login'
      return () => null
    },
  })
  const mounted = await mountInFrame(child, '/test', [
    { path: '/test', component: child },
  ])
  dispose = mounted.dispose
  const sidebar = mounted.host.querySelector('aside.sidebar')
  expect(sidebar).not.toBeNull()
  const classes = sidebar?.className
  for (const mode of ['login', 'menu', 'collapsed'] as const) {
    if (!frame) throw new Error('Missing frame context')
    frame.sidebar.value = mode
    await nextTick()
    expect(sidebar?.className).toBe(classes)
    const shell = mounted.host.querySelector('.shell')
    expect(shell?.classList.contains('sidebar-login')).toBe(mode === 'login')
    expect(shell?.classList.contains('sidebar-collapsed')).toBe(
      mode === 'collapsed',
    )
  }
})

it('keeps the layout dependency read-only for views', async () => {
  let frame: ReturnType<typeof useFrame> | undefined
  const child = defineComponent({
    setup() {
      frame = useFrame()
      return () => null
    },
  })
  const mounted = await mountInFrame(child, '/test', [
    { path: '/test', component: child },
  ])
  dispose = mounted.dispose
  const dependency = frame?.layoutDependency as Ref<number> | undefined
  if (dependency) dependency.value = 9
  expect(frame?.layoutDependency.value).toBe(0)
  frame?.moved()
  expect(frame?.layoutDependency.value).toBe(1)
})
