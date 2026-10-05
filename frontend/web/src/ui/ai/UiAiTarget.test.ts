// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, createApp, h, nextTick, ref, type Component } from 'vue'
import { createAiRegistry } from '../../ai'
import type { AiOriginRequest } from './context'
import type { AiRegistry, AiTarget } from '../../ai'
import AppLink from '../../navigation/AppLink.vue'
import { pageContext } from '../../navigation/page'
import type { PageContext } from '../../navigation/page'
import { workspaceContext } from '../../navigation/workspace'
import type { Workspace } from '../../navigation/workspace'
import { aiRegistryKey } from './context'
import UiAiTarget from './UiAiTarget.vue'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function target(id: string, extra: Partial<AiTarget> = {}): AiTarget {
  return { id, kind: 'device', label: id, context: {}, ...extra }
}

function mount(
  render: () => ReturnType<typeof h>,
  provide?: (app: ReturnType<typeof createApp>) => void,
): { host: HTMLElement; registry: AiRegistry } {
  const registry = createAiRegistry()
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ render } satisfies Component)
  app.provide(aiRegistryKey, registry)
  provide?.(app)
  app.mount(host)
  disposers.push(() => app.unmount())
  return { host, registry }
}

const page: PageContext = {
  location: computed(() => ({ path: '/devices', query: {} })),
  view: computed(() => 'devices' as const),
  deviceId: computed(() => undefined),
  primary: true,
  query: () => '',
  go: () => Promise.resolve(),
  href: (to) => `${to.path ?? '/'}`,
}

const workspace: Workspace = {
  fleet: ref([]),
  message: ref(''),
  move: ref(undefined),
  undoMove: () => {},
  dismissNotice: () => {},
  reassign: () => {},
  siteName: (id) => id,
  tenantName: (id) => id,
  follow: () => Promise.resolve(),
  activePane: ref('main'),
  peek: ref([]),
  sideDeviceId: computed(() => undefined),
}

describe('UiAiTarget', () => {
  it('renders a native li as the registered target with no wrapper', async () => {
    const { host, registry } = mount(() =>
      h('ul', [
        h(
          UiAiTarget,
          { as: 'li', class: 'row', ai: target('a:devices:device:d1') },
          { default: () => 'core-sw-1' },
        ),
      ]),
    )
    await nextTick()

    const list = host.querySelector('ul')
    expect(list?.children).toHaveLength(1)
    const item = list?.firstElementChild
    expect(item?.tagName).toBe('LI')
    expect(item?.className).toBe('row')
    expect(item?.textContent).toBe('core-sw-1')
    expect(registry.view('a:devices:device:d1')?.element).toBe(item)
  })

  it('renders a native section with its attributes and listeners on that element', async () => {
    const onClick = vi.fn()
    const { host, registry } = mount(() =>
      h(
        UiAiTarget,
        {
          as: 'section',
          'aria-label': 'Devices',
          onClick,
          ai: target('a:devices:device:d1'),
        },
        { default: () => h('p', 'body') },
      ),
    )
    await nextTick()

    const section = host.firstElementChild
    expect(section?.tagName).toBe('SECTION')
    expect(section?.getAttribute('aria-label')).toBe('Devices')
    section?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(onClick).toHaveBeenCalledOnce()
    expect(registry.view('a:devices:device:d1')?.element).toBe(section)
  })

  it('merges into an AppLink child without adding an element', async () => {
    const { host, registry } = mount(
      () =>
        h(
          UiAiTarget,
          {
            asChild: true,
            class: 'tagged',
            ai: target('a:devices:device:d1'),
          },
          {
            default: () =>
              h(AppLink, { to: { path: '/devices/d1' } }, () => 'core-sw-1'),
          },
        ),
      (app) => {
        app.provide(pageContext, page)
        app.provide(workspaceContext, workspace)
      },
    )
    await nextTick()

    expect(host.children).toHaveLength(1)
    const link = host.firstElementChild
    expect(link?.tagName).toBe('A')
    expect(link?.getAttribute('href')).toBe('/devices/d1')
    expect(link?.className).toBe('tagged')
    expect(link?.children).toHaveLength(0)
    expect(registry.view('a:devices:device:d1')?.element).toBe(link)
  })

  it('renders a div by default and registers nothing without a target', async () => {
    const { host, registry } = mount(() =>
      h(UiAiTarget, null, { default: () => 'plain' }),
    )
    await nextTick()

    expect(host.firstElementChild?.tagName).toBe('DIV')
    expect(registry.list()).toEqual([])
  })

  it('marks the origin and emits its acknowledgement from the original element', async () => {
    const origin: AiOriginRequest = {
      requestId: 'r1',
      action: 'summary',
      targets: [],
      history: [],
    }
    const onAcknowledged = vi.fn()
    const { host } = mount(() =>
      h(
        UiAiTarget,
        { as: 'li', aiOrigin: origin, onAiOriginAcknowledged: onAcknowledged },
        { default: () => 'value' },
      ),
    )
    await nextTick()
    const item = host.firstElementChild
    expect(item?.getAttribute('data-ai-origin')).toBe('agent')

    item?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()

    expect(onAcknowledged).toHaveBeenCalledExactlyOnceWith('r1')
    expect(item?.hasAttribute('data-ai-origin')).toBe(false)
  })
})
