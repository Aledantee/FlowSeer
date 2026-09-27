// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiAiSummary from './UiAiSummary.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import type { AiRequest, AiTarget } from '../../ai'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

const target: AiTarget = {
  id: 'a:dashboard:summary:scope',
  kind: 'summary',
  label: 'Berlin Mitte',
  context: { site: 'Berlin Mitte' },
}

function mount(handler?: (request: AiRequest) => Promise<string> | string) {
  const currentTarget = ref(target)
  const registry = createAiRegistry({
    viewport: () => ({ wide: true, narrow: false }),
  })
  const element = document.createElement('div')
  document.body.append(element)
  registry.register(element, currentTarget.value)
  if (handler) registry.onRequest(handler)

  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () => h(UiAiSummary, { target: currentTarget.value }),
  })
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  disposers.push(() => app.unmount())
  return {
    registry,
    element,
    host,
    setTarget(next: AiTarget) {
      currentTarget.value = next
      registry.register(element, next)
    },
  }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 10))
  await nextTick()
}

function button(label: string) {
  return [...document.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label,
  )
}

describe('UiAiSummary', () => {
  it('makes no request until Generate summary is activated', async () => {
    const handler = vi.fn<(request: AiRequest) => Promise<string>>(
      async () => 'A summary',
    )
    mount(handler)

    await settle()
    expect(handler).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Generate summary')

    button('Generate summary')?.click()
    await settle()

    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler.mock.calls[0]?.[0]).toMatchObject({
      kind: 'summary',
      targetId: target.id,
    })
  })

  it('renders a shimmering pending state with an accessible status', async () => {
    mount(() => new Promise<string>(() => {}))
    await settle()
    button('Generate summary')?.click()
    await settle()

    const bars = document.querySelectorAll('.ai-shimmer')
    expect(bars.length).toBeGreaterThan(0)
    const status = document.querySelector('[role="status"]')
    expect(status?.textContent).toContain('Generating summary')
    expect(status?.classList).toContain('motion-reduce:not-sr-only')
  })

  it('shows a result and stops the pending state', async () => {
    mount(async () => 'Two access points stopped answering.')
    await settle()
    button('Generate summary')?.click()
    await settle()

    expect(document.body.textContent).toContain(
      'Two access points stopped answering.',
    )
    expect(document.querySelectorAll('.ai-shimmer')).toHaveLength(0)
  })

  it('shows an error and retries with a new request id', async () => {
    const requests: AiRequest[] = []
    mount(async (request) => {
      requests.push(request)
      throw new Error('The summary service did not answer.')
    })

    await settle()
    button('Generate summary')?.click()
    await settle()
    expect(document.querySelector('[role="alert"]')?.textContent).toContain(
      'The summary service did not answer.',
    )

    const retry = button('Retry')
    expect(retry, 'Expected an error state to offer Retry').toBeDefined()
    retry?.click()
    await settle()

    await vi.waitFor(() => expect(requests).toHaveLength(2))
    expect(requests[1]?.requestId).not.toBe(requests[0]?.requestId)
  })

  it('reports the unavailable state with no handler', async () => {
    mount()
    await settle()
    button('Generate summary')?.click()
    await settle()
    expect(document.body.textContent).toContain('AI is unavailable')
  })

  it('discards an answer whose target unmounted before it resolved', async () => {
    let release: ((value: string) => void) | undefined
    const { registry, element } = mount(
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )

    await settle()
    button('Generate summary')?.click()
    await settle()
    registry.unregister(element)
    release?.('A late summary')
    await settle()

    expect(document.body.textContent).not.toContain('A late summary')
    expect(button('Generate summary')).toBeDefined()
  })

  it('resets a result when the same target id gains new context', async () => {
    const requests: AiRequest[] = []
    const { setTarget } = mount((request) => {
      requests.push(request)
      return `Summary for ${request.context.site}`
    })

    await settle()
    button('Generate summary')?.click()
    await settle()
    expect(document.body.textContent).toContain('Summary for Berlin Mitte')

    setTarget({ ...target, context: { site: 'Hamburg Hafen' } })
    await settle()
    expect(document.body.textContent).not.toContain('Summary for Berlin Mitte')
    expect(button('Generate summary')).toBeDefined()

    button('Generate summary')?.click()
    await settle()
    expect(document.body.textContent).toContain('Summary for Hamburg Hafen')
    expect(requests[1]?.context).toEqual({ site: 'Hamburg Hafen' })
  })

  it('ignores a late answer after the same target id changes context', async () => {
    let releaseOld: ((answer: string) => void) | undefined
    const { setTarget } = mount((request) => {
      if (request.context.site === 'Berlin Mitte')
        return new Promise<string>((resolve) => {
          releaseOld = resolve
        })
      return 'Current summary'
    })

    await settle()
    button('Generate summary')?.click()
    await settle()
    setTarget({ ...target, context: { site: 'Hamburg Hafen' } })
    await settle()
    button('Generate summary')?.click()
    await settle()
    expect(document.body.textContent).toContain('Current summary')

    releaseOld?.('Outdated summary')
    await settle()
    expect(document.body.textContent).toContain('Current summary')
    expect(document.body.textContent).not.toContain('Outdated summary')
  })
})
