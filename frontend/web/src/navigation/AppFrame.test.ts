// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref, Teleport } from 'vue'
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

it('starts with a login sidebar and a 54px top bar', async () => {
  const child = defineComponent({
    setup() {
      const frame = useFrame()
      frame.sidebar.value = 'login'
      return () => h('span', frame.topbarHeight.value)
    },
  })
  const mounted = await mountInFrame(child, '/test', [
    { path: '/test', component: child },
  ])
  dispose = mounted.dispose
  await nextTick()
  expect(mounted.host.querySelector('aside')?.id).toBe('')
  expect(mounted.host.querySelector('.main-notch')).toBeNull()
  expect(mounted.host.querySelector('#frame-topbar')?.textContent).toBe('')
  expect(mounted.host.querySelector('span')?.textContent).toBe('54')
})
