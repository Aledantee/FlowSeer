// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiAiSummary, {
  type UiAiSummaryLabels,
  type UiAiSummaryProps,
} from './UiAiSummary.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import type { AiRequest, AiResult, AiTarget } from '../../ai'
import { createWebI18n, type WebLocale } from '../../i18n'

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

function mount(
  handler?: (request: AiRequest) => Promise<unknown> | unknown,
  locale: WebLocale = 'en',
  props?: Partial<UiAiSummaryProps>,
) {
  const currentTarget = ref(props?.target ?? target)
  const registry = createAiRegistry({
    viewport: () => ({ wide: true, narrow: false }),
  })
  const element = document.createElement('div')
  document.body.append(element)
  registry.register(element, currentTarget.value)
  if (handler) {
    registry.onRequest((req) => {
      const res = handler(req)
      if (
        typeof res === 'object' &&
        res !== null &&
        Symbol.asyncIterator in res
      ) {
        return res as AsyncIterable<AiResult>
      }
      return Promise.resolve(res).then((r) => {
        if (typeof r === 'string') {
          return {
            type: 'summary',
            headline: r,
            tone: 'ok',
            findings: [],
            metrics: [],
            next: [],
            sources: [],
          }
        }
        return r as AiResult
      })
    })
  }

  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n(locale)
  const app = createApp({
    render: () =>
      h(UiAiSummary, {
        target: currentTarget.value,
        labels: props?.labels,
      }),
  })
  app.use(i18n)
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  disposers.push(() => app.unmount())
  return {
    registry,
    element,
    host,
    i18n,
    setTarget(next: AiTarget) {
      currentTarget.value = next
      registry.register(element, next)
    },
  }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
  await nextTick()
}

function button(label: string) {
  return [...document.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label,
  )
}

describe('UiAiSummary', () => {
  it('starts as a labelled button ("Summarize <label>") and makes no request until activated', async () => {
    const handler = vi.fn<(request: AiRequest) => Promise<string>>(
      async () => 'A summary',
    )
    mount(handler)

    await settle()
    expect(handler).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Summarize Berlin Mitte')

    button('Summarize Berlin Mitte')?.click()
    await settle()

    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler.mock.calls[0]?.[0]).toMatchObject({
      action: 'summary',
      targets: [expect.objectContaining({ id: target.id })],
    })
  })

  it('renders a shimmering pending state with aria-busy and accessible status', async () => {
    mount(() => new Promise<string>(() => {}))
    await settle()
    button('Summarize Berlin Mitte')?.click()
    await settle()

    const bars = document.querySelectorAll('.ai-shimmer')
    expect(bars.length).toBeGreaterThan(0)
    const resultRoot = document.querySelector('[data-ai-result]')
    expect(resultRoot?.getAttribute('aria-busy')).toBe('true')
    const status = document.querySelector('[role="status"]')
    expect(status?.textContent).toContain('Generating summary…')
  })

  it('shows a structured result and renders UiAiLabel and UiAiResultActions', async () => {
    mount(async () => 'Two access points stopped answering.')
    await settle()
    button('Summarize Berlin Mitte')?.click()
    await settle()

    expect(document.body.textContent).toContain(
      'Two access points stopped answering.',
    )
    expect(document.querySelectorAll('.ai-shimmer')).toHaveLength(0)
    expect(document.querySelector('[data-ai-label-trigger]')).not.toBeNull()
    expect(document.querySelector('[data-ai-result-actions]')).not.toBeNull()
  })

  it('handles Stop during generation and keeps the last snapshot marked Stopped', async () => {
    let resolveSecond: (() => void) | undefined
    const secondPromise = new Promise<void>((r) => {
      resolveSecond = r
    })

    async function* generateSnapshots() {
      yield {
        type: 'summary',
        headline: 'First snapshot arrived',
        tone: 'ok',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      } as AiResult
      await secondPromise
      yield {
        type: 'summary',
        headline: 'Final snapshot arrived',
        tone: 'ok',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      } as AiResult
    }

    mount(() => generateSnapshots())
    await settle()
    button('Summarize Berlin Mitte')?.click()
    await settle()

    expect(document.body.textContent).toContain('First snapshot arrived')

    const stopBtn = button('Stop')
    expect(stopBtn).toBeDefined()
    stopBtn?.click()
    await settle()

    expect(document.body.textContent).toContain('Stopped')
    expect(document.body.textContent).toContain('First snapshot arrived')

    resolveSecond?.()
    await settle()
    expect(document.body.textContent).not.toContain('Final snapshot arrived')
  })

  it('keeps a finished result while the target context updates, and resets only when target id changes', async () => {
    const requests: AiRequest[] = []
    const { setTarget } = mount((request) => {
      requests.push(request)
      return `Summary for ${request.targets[0]?.context.site}`
    })

    await settle()
    button('Summarize Berlin Mitte')?.click()
    await settle()
    expect(document.body.textContent).toContain('Summary for Berlin Mitte')

    // Context changes on same target ID: result is KEPT
    setTarget({ ...target, context: { site: 'Hamburg Hafen' } })
    await settle()
    expect(document.body.textContent).toContain('Summary for Berlin Mitte')
    expect(button('Summarize Berlin Mitte')).toBeUndefined()

    // Target ID changes: result RESETS to idle
    setTarget({
      id: 'a:dashboard:summary:different',
      kind: 'summary',
      label: 'Munich Nord',
      context: { site: 'Munich Nord' },
    })
    await settle()
    expect(document.body.textContent).not.toContain('Summary for Berlin Mitte')
    expect(button('Summarize Munich Nord')).toBeDefined()
  })

  it('discards an answer whose target element unmounted before it resolved (AiStaleError)', async () => {
    let release: ((value: string) => void) | undefined
    const { registry, element } = mount(
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )

    await settle()
    button('Summarize Berlin Mitte')?.click()
    await settle()

    // Unregister element while bound run is in progress
    registry.unregister(element)
    release?.('A late summary')
    await settle()

    // Resets to idle
    expect(document.body.textContent).not.toContain('A late summary')
    expect(button('Summarize Berlin Mitte')).toBeDefined()
  })

  it('shows an error and retries with a new request id', async () => {
    const requests: AiRequest[] = []
    mount(async (request) => {
      requests.push(request)
      throw new Error('The summary service did not answer.')
    })

    await settle()
    button('Summarize Berlin Mitte')?.click()
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
    button('Summarize Berlin Mitte')?.click()
    await settle()
    expect(document.body.textContent).toContain('AI is unavailable')
  })

  it('supports German (de) localization and custom label overrides', async () => {
    mount(async () => 'Ergebnis.', 'de')
    await settle()
    expect(document.body.textContent).toContain(
      'Zusammenfassung für Berlin Mitte',
    )

    button('Zusammenfassung für Berlin Mitte')?.click()
    await settle()
    expect(document.body.textContent).toContain('Ergebnis.')

    for (const d of disposers) d()
    disposers = []
    document.body.replaceChildren()

    // Custom label override
    const customLabels: UiAiSummaryLabels = {
      summaryLabel: 'Custom AI Section',
      generate: 'Custom Run Summary',
      retry: 'Custom Try Again',
    }
    mount(async () => 'Custom text', 'en', { labels: customLabels })
    await settle()
    expect(document.body.textContent).toContain('Custom Run Summary')
    expect(document.querySelector('section')?.getAttribute('aria-label')).toBe(
      'Custom AI Section',
    )

    button('Custom Run Summary')?.click()
    await settle()
    expect(document.body.textContent).toContain('Custom text')
  })
})
