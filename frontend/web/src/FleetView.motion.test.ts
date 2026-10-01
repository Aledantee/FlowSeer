// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { createAiRegistry, createAiTargetDirective } from './ai'
import type { AiRegistry } from './ai'
import { UiAppRoot } from './ui'
import { aiRegistryKey } from './ui/ai/context'

let dispose = () => {}
let preference: (EventTarget & { matches: boolean }) | undefined
const mediaQueries = new Map<string, EventTarget & { matches: boolean }>()

beforeEach(() => {
  mediaQueries.clear()
  vi.stubGlobal('matchMedia', (query: string) => {
    const existing = mediaQueries.get(query)
    if (existing) return existing
    const media = Object.assign(new EventTarget(), {
      matches: query.includes('min-width'),
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
    }) as EventTarget & { matches: boolean }
    mediaQueries.set(query, media)
    if (query.includes('prefers-reduced-motion')) preference = media
    return media
  })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const element = this instanceof HTMLElement ? this : undefined
      const collapsed = element
        ?.closest('.shell')
        ?.classList.contains('sidebar-collapsed')
      const isMainShell = element?.classList.contains('main-shell')
      const width = collapsed ? 64 : 204
      const left = isMainShell ? width : 0
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
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function wait(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

async function mountFleet() {
  const host = document.createElement('div')
  document.body.append(host)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/:view(dashboard|devices|clients|sites|topology)',
        component: FleetView,
      },
      { path: '/devices/:deviceId', component: FleetView },
    ],
  })
  const registry: AiRegistry = createAiRegistry()
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () => h(FleetView))
    },
  })
  await router.push('/dashboard')
  app.use(router)
  app.directive('ai-target', createAiTargetDirective(registry))
  app.provide(aiRegistryKey, registry)
  await router.isReady()
  app.mount(host)
  dispose = () => app.unmount()
  await nextTick()
  await wait(20)
  return host
}

describe('FleetView motion layout', () => {
  it('animates the sidebar size and main-shell position', async () => {
    const host = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    toggle?.click()
    await nextTick()
    await wait(30)

    expect(
      host.querySelector<HTMLElement>('.sidebar')?.style.transform,
    ).toContain('scale(')
    expect(
      host.querySelector<HTMLElement>('.main-shell')?.style.transform,
    ).toContain('translate')
    expect(
      host.querySelector<HTMLElement>('.main-shell')?.style.transform,
    ).not.toContain('scale(')
  })

  it('stops layout transforms after the user enables reduced motion', async () => {
    const host = await mountFleet()
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')

    if (!preference) throw new Error('Missing reduced-motion media query')
    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()

    toggle?.click()
    await nextTick()
    await wait(30)

    expect(host.querySelector<HTMLElement>('.sidebar')?.style.transform).toBe(
      '',
    )
    expect(
      host.querySelector<HTMLElement>('.main-shell')?.style.transform,
    ).toBe('')
  })
})
