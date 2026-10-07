// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref, Teleport } from 'vue'
import type { Ref } from 'vue'
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

it('keeps four regions in one frame and clears teleported content on unmount', async () => {
  const visible = ref(true)
  const child = defineComponent({
    setup() {
      const frame = useFrame()
      frame.sidebar.value = 'collapsed'
      return () =>
        visible.value
          ? ['skip', 'sidebar', 'topbar', 'page'].map((region) =>
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
  for (const region of ['skip', 'sidebar', 'topbar', 'page'])
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
