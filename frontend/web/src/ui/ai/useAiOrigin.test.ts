// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  createApp,
  defineComponent,
  h,
  nextTick,
  reactive,
  ref,
  type Component,
} from 'vue'
import { createAiRegistry } from '../../ai'
import type { AiRegistry, AiTarget } from '../../ai'
import { aiRegistryKey } from './context'
import type { AiOriginRequest } from './context'
import { useAiOrigin } from './useAiOrigin'
import type { AiOrigin } from './useAiOrigin'
import { useAiTarget } from './useAiTarget'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function origin(requestId: string): AiOriginRequest {
  return { requestId, action: 'summary', targets: [], history: [] }
}

function target(id: string): AiTarget {
  return { id, kind: 'device', label: id, context: {} }
}

interface State {
  origin?: AiOriginRequest
  ai?: AiTarget
}

interface Mounted {
  host: HTMLElement
  registry: AiRegistry
  acknowledged: string[]
  handle: () => AiOrigin
  unmount: () => void
}

function mountRoot(render: (registry: AiRegistry) => Component) {
  const registry = createAiRegistry()
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp(render(registry))
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  let mounted = true
  const unmount = () => {
    if (!mounted) return
    mounted = false
    app.unmount()
  }
  disposers.push(unmount)
  return { host, registry, unmount }
}

// A component that marks its own input, as a Ui control does.
function mountValue(state: State): Mounted {
  const acknowledged: string[] = []
  let handle: AiOrigin | undefined
  const { host, registry, unmount } = mountRoot(() =>
    defineComponent({
      setup() {
        const anchor = ref<HTMLElement>()
        useAiTarget(anchor, () => state.ai)
        handle = useAiOrigin(
          anchor,
          () => state.origin,
          (requestId) => acknowledged.push(requestId),
        )
        return () => h('input', { ref: anchor, id: 'value' })
      },
    }),
  )
  return {
    host,
    registry,
    acknowledged,
    handle: () => {
      if (!handle) throw new Error('The hook has not run')
      return handle
    },
    unmount,
  }
}

function input(host: HTMLElement): HTMLInputElement {
  const node = host.querySelector('input')
  if (!node) throw new Error('Missing input')
  return node
}

const marker = (node: Element) => node.getAttribute('data-ai-origin')

describe('useAiOrigin', () => {
  it('marks the element with the origin even when it registers no target', async () => {
    const { host, registry } = mountValue(reactive({ origin: origin('r1') }))
    await nextTick()

    expect(marker(input(host))).toBe('agent')
    expect(registry.list()).toEqual([])
  })

  it('writes no marker without an origin', async () => {
    const { host } = mountValue(reactive({}))
    await nextTick()

    expect(input(host).hasAttribute('data-ai-origin')).toBe(false)
  })

  it('keeps the marker through highlighting, hover, focus, and a programmatic value', async () => {
    const state = reactive<State>({
      origin: origin('r1'),
      ai: target('a:devices:device:d1'),
    })
    const { host, registry, acknowledged } = mountValue(state)
    await nextTick()
    const node = input(host)

    registry.highlight('a:devices:device:d1')
    node.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
    node.dispatchEvent(new MouseEvent('mouseenter'))
    node.focus()
    node.value = 'set by an agent'
    await nextTick()

    expect(marker(node)).toBe('agent')
    expect(node.hasAttribute('data-ai-selected')).toBe(true)
    expect(acknowledged).toEqual([])
  })

  it.each(['pointerdown', 'keydown', 'input', 'change'])(
    'acknowledges the request once on a %s',
    async (type) => {
      const { host, acknowledged } = mountValue(
        reactive({ origin: origin('r1') }),
      )
      await nextTick()
      const node = input(host)

      node.dispatchEvent(new Event(type, { bubbles: true }))
      node.dispatchEvent(new Event(type, { bubbles: true }))
      await nextTick()

      expect(acknowledged).toEqual(['r1'])
      expect(node.hasAttribute('data-ai-origin')).toBe(false)
    },
  )

  it('stays quiet when the same request renders again and marks a new request', async () => {
    const state = reactive<State>({ origin: origin('r1') })
    const { host, acknowledged } = mountValue(state)
    await nextTick()
    const node = input(host)
    node.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()

    state.origin = origin('r1')
    await nextTick()
    expect(node.hasAttribute('data-ai-origin')).toBe(false)

    state.origin = origin('r2')
    await nextTick()
    expect(marker(node)).toBe('agent')

    node.dispatchEvent(new Event('keydown', { bubbles: true }))
    await nextTick()
    expect(acknowledged).toEqual(['r1', 'r2'])
    expect(node.hasAttribute('data-ai-origin')).toBe(false)
  })

  it('forgets an acknowledged request once the caller clears the origin', async () => {
    const state = reactive<State>({ origin: origin('r1') })
    const { host, acknowledged } = mountValue(state)
    await nextTick()
    const node = input(host)
    node.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()

    state.origin = undefined
    await nextTick()
    state.origin = origin('r1')
    await nextTick()
    expect(marker(node)).toBe('agent')

    node.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(acknowledged).toEqual(['r1', 'r1'])
  })

  it('removes the marker and its listeners when the caller clears the origin', async () => {
    const state = reactive<State>({ origin: origin('r1') })
    const { host, acknowledged } = mountValue(state)
    await nextTick()
    const node = input(host)
    const removed = vi.spyOn(node, 'removeEventListener')

    state.origin = undefined
    await nextTick()
    node.dispatchEvent(new Event('pointerdown', { bubbles: true }))

    expect(node.hasAttribute('data-ai-origin')).toBe(false)
    expect(acknowledged).toEqual([])
    expect(removed).toHaveBeenCalledWith(
      'pointerdown',
      expect.any(Function),
      true,
    )
  })

  it('removes the marker and listens no more after unmount', async () => {
    const { host, acknowledged, unmount } = mountValue(
      reactive({ origin: origin('r1') }),
    )
    await nextTick()
    const node = input(host)

    unmount()
    node.dispatchEvent(new Event('pointerdown', { bubbles: true }))

    expect(node.hasAttribute('data-ai-origin')).toBe(false)
    expect(acknowledged).toEqual([])
  })

  it('keeps the selection when the origin is acknowledged', async () => {
    const state = reactive<State>({
      origin: origin('r1'),
      ai: target('a:devices:device:d1'),
    })
    const { host, registry } = mountValue(state)
    await nextTick()
    const node = input(host)
    registry.highlight('a:devices:device:d1')

    node.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()

    expect(node.hasAttribute('data-ai-origin')).toBe(false)
    expect(node.hasAttribute('data-ai-selected')).toBe(true)
  })

  it('acknowledges through the returned handler for portalled interactions', async () => {
    const { host, acknowledged, handle } = mountValue(
      reactive({ origin: origin('r1') }),
    )
    await nextTick()

    handle().acknowledge()
    handle().acknowledge()
    await nextTick()

    expect(acknowledged).toEqual(['r1'])
    expect(handle().active.value).toBeUndefined()
    expect(input(host).hasAttribute('data-ai-origin')).toBe(false)
  })

  it('reports the active request through the returned state', async () => {
    const state = reactive<State>({ origin: origin('r1') })
    const { handle } = mountValue(state)
    await nextTick()
    expect(handle().active.value?.requestId).toBe('r1')

    state.origin = undefined
    await nextTick()
    expect(handle().active.value).toBeUndefined()
  })

  it('keeps nested origins independent', async () => {
    const outer = reactive<State>({ origin: origin('outer') })
    const inner = reactive<State>({ origin: origin('inner') })
    const acknowledged: string[] = []
    const Inner = defineComponent({
      setup() {
        const anchor = ref<HTMLElement>()
        useAiOrigin(
          anchor,
          () => inner.origin,
          (requestId) => acknowledged.push(requestId),
        )
        return () => h('input', { ref: anchor, id: 'inner' })
      },
    })
    const { host } = mountRoot(() =>
      defineComponent({
        setup() {
          const anchor = ref<HTMLElement>()
          useAiOrigin(
            anchor,
            () => outer.origin,
            (requestId) => acknowledged.push(requestId),
          )
          return () => h('section', { ref: anchor, id: 'outer' }, [h(Inner)])
        },
      }),
    )
    await nextTick()
    const section = host.querySelector('#outer')
    const field = host.querySelector('#inner')
    expect(section && marker(section)).toBe('agent')
    expect(field && marker(field)).toBe('agent')

    field?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(acknowledged).toEqual(['inner'])
    expect(field?.hasAttribute('data-ai-origin')).toBe(false)
    expect(section && marker(section)).toBe('agent')

    section?.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(acknowledged).toEqual(['inner', 'outer'])
    expect(section?.hasAttribute('data-ai-origin')).toBe(false)
  })
})
