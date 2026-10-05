// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import {
  createApp,
  defineComponent,
  h,
  nextTick,
  reactive,
  ref,
  type Component,
} from 'vue'
import { aiRegistry, createAiRegistry } from '../../ai'
import type { AiRegistry, AiSummary, AiTarget } from '../../ai'
import { aiRegistryKey } from './context'
import { useAiTarget } from './useAiTarget'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function target(id: string, extra: Partial<AiTarget> = {}): AiTarget {
  return { id, kind: 'device', label: id, context: { entity: id }, ...extra }
}

function ids(registry: AiRegistry): string[] {
  return registry.list().map((item) => item.id)
}

// The registry hides a detached element, so a removed registration is only
// proven gone by attaching the element to the document again.
function unmounted(registry: AiRegistry, element: Element | null): string[] {
  if (element) document.body.append(element)
  return ids(registry)
}

interface Mounted {
  host: HTMLElement
  unmount: () => void
}

function mount(registry: AiRegistry | undefined, root: Component): Mounted {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp(root)
  if (registry) app.provide(aiRegistryKey, registry)
  app.mount(host)
  let mounted = true
  const unmount = () => {
    if (!mounted) return
    mounted = false
    app.unmount()
  }
  disposers.push(unmount)
  return { host, unmount }
}

// A component that registers its own root element, the way a Ui component does.
function probe(state: { ai?: AiTarget; tag?: string }): Component {
  return defineComponent({
    setup() {
      const anchor = ref<HTMLElement>()
      useAiTarget(anchor, () => state.ai)
      return () => h(state.tag ?? 'div', { ref: anchor, id: 'row' })
    },
  })
}

const summary: AiSummary = {
  type: 'summary',
  headline: 'Healthy',
  tone: 'ok',
  findings: [],
  metrics: [],
  next: [],
  sources: [],
}

describe('useAiTarget', () => {
  it('registers nothing without a target and follows the target as it changes', async () => {
    const registry = createAiRegistry()
    const state = reactive<{ ai?: AiTarget }>({})
    const { host, unmount } = mount(registry, probe(state))
    await nextTick()
    expect(ids(registry)).toEqual([])

    state.ai = target('a:devices:device:d1')
    await nextTick()
    expect(ids(registry)).toEqual(['a:devices:device:d1'])

    state.ai = target('a:devices:device:d2')
    await nextTick()
    expect(ids(registry)).toEqual(['a:devices:device:d2'])

    state.ai = undefined
    await nextTick()
    expect(ids(registry)).toEqual([])

    state.ai = target('a:devices:device:d3')
    await nextTick()
    expect(ids(registry)).toEqual(['a:devices:device:d3'])
    const element = host.querySelector('#row')
    unmount()
    expect(unmounted(registry, element)).toEqual([])
  })

  it('registers the rendered element that the ref holds', async () => {
    const registry = createAiRegistry()
    const state = reactive({ ai: target('a:devices:device:d1') })
    const { host } = mount(registry, probe(state))
    await nextTick()

    expect(registry.view('a:devices:device:d1')?.element).toBe(
      host.querySelector('#row'),
    )
  })

  it('sees an in-place edit of a reactive target object', async () => {
    const registry = createAiRegistry()
    const state = reactive({
      ai: reactive(
        target('a:devices:device:d1', { context: { health: 'Up' } }),
      ),
    })
    mount(registry, probe(state))
    await nextTick()
    expect(registry.list()[0]?.context).toEqual({ health: 'Up' })

    state.ai.label = 'renamed'
    state.ai.context.health = 'Down'
    await nextTick()

    expect(registry.list()[0]?.label).toBe('renamed')
    expect(registry.list()[0]?.context).toEqual({ health: 'Down' })
  })

  it('keeps a bound request when an equal target object replaces the old one', async () => {
    const registry = createAiRegistry()
    const state = reactive({ ai: target('a:devices:device:d1') })
    mount(registry, probe(state))
    await nextTick()
    let resolve: ((result: AiSummary) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<AiSummary>((done) => {
          resolve = done
        }),
    )
    const run = registry.request(state.ai, { action: 'summary', bound: true })
    const results: unknown[] = []
    const consumed = (async () => {
      for await (const result of run.snapshots) results.push(result)
    })()

    state.ai = target('a:devices:device:d1')
    await nextTick()
    resolve?.(summary)
    await consumed

    expect(results).toEqual([summary])
  })

  it('replaces the registration when the identity changes and ends a bound request', async () => {
    const registry = createAiRegistry()
    const state = reactive({ ai: target('a:devices:device:d1') })
    mount(registry, probe(state))
    await nextTick()
    registry.onRequest(() => new Promise<AiSummary>(() => {}))
    const run = registry.request(state.ai, { action: 'summary', bound: true })

    state.ai = target('a:devices:device:d2')
    await nextTick()

    expect(ids(registry)).toEqual(['a:devices:device:d2'])
    await expect(async () => {
      for await (const result of run.snapshots) void result
    }).rejects.toThrow('no longer mounted')
  })

  it('drops the old element and its selection when the rendered element is replaced', async () => {
    const registry = createAiRegistry()
    const state = reactive({ ai: target('a:devices:device:d1'), tag: 'div' })
    const { host } = mount(registry, probe(state))
    await nextTick()
    const before = host.querySelector('#row')
    registry.highlight('a:devices:device:d1')
    expect(before?.hasAttribute('data-ai-selected')).toBe(true)

    state.tag = 'section'
    await nextTick()

    const after = host.querySelector('#row')
    expect(after).not.toBe(before)
    expect(before?.hasAttribute('data-ai-selected')).toBe(false)
    expect(registry.view('a:devices:device:d1')?.element).toBe(after)
    expect(ids(registry)).toEqual(['a:devices:device:d1'])
  })

  it('reconciles a forwarded component root that changes on the owner update', async () => {
    const registry = createAiRegistry()
    const state = reactive({ tag: 'div' })
    const Child = defineComponent({
      props: { tag: { type: String, required: true } },
      setup(props) {
        return () => h(props.tag, { id: 'forwarded' })
      },
    })
    const Owner = defineComponent({
      setup() {
        const child = ref()
        useAiTarget(child, () => target('a:devices:device:d1'))
        return () => h(Child, { ref: child, tag: state.tag })
      },
    })
    const { host } = mount(registry, Owner)
    await nextTick()
    const first = host.querySelector('#forwarded')
    expect(registry.view('a:devices:device:d1')?.element).toBe(first)

    state.tag = 'section'
    await nextTick()

    const second = host.querySelector('#forwarded')
    expect(second).not.toBe(first)
    expect(registry.view('a:devices:device:d1')?.element).toBe(second)
  })

  it('registers nothing for a component that renders no element', async () => {
    const registry = createAiRegistry()
    const Empty = defineComponent({ setup: () => () => null })
    const Owner = defineComponent({
      setup() {
        const child = ref()
        useAiTarget(child, () => target('a:devices:device:d1'))
        return () => h(Empty, { ref: child })
      },
    })
    mount(registry, Owner)
    await nextTick()

    expect(ids(registry)).toEqual([])
  })

  it('unregisters on unmount and leaves another component registered', async () => {
    const registry = createAiRegistry()
    const state = reactive({ show: true })
    const first = probe({ ai: target('a:devices:device:d1') })
    const second = probe({ ai: target('a:devices:device:d2') })
    const { host } = mount(
      registry,
      defineComponent({
        setup: () => () => [state.show ? h(first) : null, h(second)],
      }),
    )
    await nextTick()
    expect(ids(registry)).toEqual([
      'a:devices:device:d1',
      'a:devices:device:d2',
    ])
    const removed = host.querySelector('#row')

    state.show = false
    await nextTick()

    expect(unmounted(registry, removed)).toEqual(['a:devices:device:d2'])
  })

  it('falls back to the document registry when none is provided', async () => {
    const { host, unmount } = mount(
      undefined,
      probe({ ai: target('story:d1') }),
    )
    await nextTick()
    expect(ids(aiRegistry)).toEqual(['story:d1'])
    const element = host.querySelector('#row')

    unmount()

    expect(unmounted(aiRegistry, element)).toEqual([])
  })

  it('writes to the injected registry of its own app and leaves the others alone', async () => {
    const one = createAiRegistry()
    const two = createAiRegistry()
    const mountedOne = mount(one, probe({ ai: target('a:devices:device:d1') }))
    mount(two, probe({ ai: target('a:devices:device:d2') }))
    await nextTick()
    expect(ids(one)).toEqual(['a:devices:device:d1'])
    expect(ids(two)).toEqual(['a:devices:device:d2'])
    const element = mountedOne.host.querySelector('#row')

    mountedOne.unmount()

    expect(unmounted(one, element)).toEqual([])
    expect(ids(two)).toEqual(['a:devices:device:d2'])
  })
})
