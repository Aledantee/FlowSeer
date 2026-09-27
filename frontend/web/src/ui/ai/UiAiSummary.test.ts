// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
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
  const registry = createAiRegistry({
    viewport: () => ({ wide: true, narrow: false }),
  })
  const element = document.createElement('div')
  document.body.append(element)
  registry.register(element, target)
  if (handler) registry.onRequest(handler)

  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ render: () => h(UiAiSummary, { target }) })
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  disposers.push(() => app.unmount())
  return { registry, element, host }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
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

    button('Retry')?.click()
    await settle()

    expect(requests).toHaveLength(2)
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
})
