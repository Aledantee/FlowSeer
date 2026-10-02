// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiAiSummary, { type UiAiSummaryProps } from './UiAiSummary.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import { AiUnavailableError } from '../../ai'
import type { AiRequest, AiTarget } from '../../ai'
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
  handler?: (request: AiRequest) => Promise<string> | string,
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
  if (handler) registry.onRequest(handler)

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

  it('covers idle, loading, result, error, unavailable, and retry in German (de)', async () => {
    // Idle
    const handler = vi.fn(async () => 'Ergebnis-Zusammenfassung.')
    mount(handler, 'de')
    await settle()
    expect(document.body.textContent).toContain('Zusammenfassung erstellen')
    expect(button('Zusammenfassung erstellen')).toBeDefined()

    // Loading and Result
    button('Zusammenfassung erstellen')?.click()
    await nextTick()
    const status = document.querySelector('[role="status"]')
    expect(status?.textContent).toContain('Zusammenfassung wird erstellt…')
    await settle()
    expect(document.body.textContent).toContain('Ergebnis-Zusammenfassung.')

    // Unavailable
    for (const d of disposers) d()
    disposers = []
    document.body.replaceChildren()

    mount(undefined, 'de')
    await settle()
    button('Zusammenfassung erstellen')?.click()
    await settle()
    expect(document.body.textContent).toContain('KI ist nicht verfügbar')
    expect(button('Wiederholen')).toBeDefined()

    // Error (Error instance)
    for (const d of disposers) d()
    disposers = []
    document.body.replaceChildren()

    const errRequests: AiRequest[] = []
    mount(async (req) => {
      errRequests.push(req)
      throw new Error('Benutzerdefinierter Fehler')
    }, 'de')
    await settle()
    button('Zusammenfassung erstellen')?.click()
    await settle()
    expect(document.querySelector('[role="alert"]')?.textContent).toContain(
      'Benutzerdefinierter Fehler',
    )
    const retry = button('Wiederholen')
    expect(retry).toBeDefined()
    retry?.click()
    await settle()
    await vi.waitFor(() => expect(errRequests).toHaveLength(2))

    // Generic error (non-Error)
    for (const d of disposers) d()
    disposers = []
    document.body.replaceChildren()

    mount(async () => {
      throw 'non-error failure'
    }, 'de')
    await settle()
    button('Zusammenfassung erstellen')?.click()
    await settle()
    expect(document.querySelector('[role="alert"]')?.textContent).toContain(
      'Etwas ist schiefgelaufen.',
    )
    expect(button('Wiederholen')).toBeDefined()
  })

  it('interpolates target label into summaryLabel aria-label in en and de', async () => {
    mount(undefined, 'en')
    await settle()
    expect(document.querySelector('section')?.getAttribute('aria-label')).toBe(
      'AI summary for Berlin Mitte',
    )

    mount(undefined, 'de')
    await settle()
    const sections = document.querySelectorAll('section')
    const lastSection = sections[sections.length - 1]
    expect(lastSection?.getAttribute('aria-label')).toBe(
      'KI-Zusammenfassung für Berlin Mitte',
    )
  })

  it('overrides every label when labels prop is provided', async () => {
    mount(async () => 'Custom answer', 'en', {
      labels: {
        summaryLabel: 'Custom summary for target',
        generate: 'Generate brief',
        generating: 'Generating brief…',
        unavailable: 'Service unavailable',
        retry: 'Try again',
        error: 'Failed to generate brief',
      },
    })
    await settle()
    expect(document.querySelector('section')?.getAttribute('aria-label')).toBe(
      'Custom summary for target',
    )
    expect(button('Generate brief')).toBeDefined()

    button('Generate brief')?.click()
    await nextTick()
    expect(document.querySelector('[role="status"]')?.textContent).toContain(
      'Generating brief…',
    )
    await settle()
    expect(document.body.textContent).toContain('Custom answer')

    // Test unavailable with custom labels
    for (const d of disposers) d()
    disposers = []
    document.body.replaceChildren()

    mount(
      async () => {
        throw new AiUnavailableError()
      },
      'en',
      {
        labels: {
          unavailable: 'Service unavailable',
          retry: 'Try again',
        },
      },
    )
    await settle()
    button('Generate summary')?.click()
    await settle()
    expect(document.body.textContent).toContain('Service unavailable')
    expect(button('Try again')).toBeDefined()

    // Test generic error with custom labels
    for (const d of disposers) d()
    disposers = []
    document.body.replaceChildren()

    mount(
      async () => {
        throw 'non-error failure'
      },
      'en',
      {
        labels: {
          error: 'Failed to generate brief',
          retry: 'Try again',
        },
      },
    )
    await settle()
    button('Generate summary')?.click()
    await settle()
    expect(document.querySelector('[role="alert"]')?.textContent).toContain(
      'Failed to generate brief',
    )
    expect(button('Try again')).toBeDefined()
  })

  it('updates loading status text on locale switch during a pending request', async () => {
    let resolvePending: ((val: string) => void) | undefined
    const { i18n } = mount(
      () =>
        new Promise<string>((resolve) => {
          resolvePending = resolve
        }),
      'en',
    )
    await settle()
    button('Generate summary')?.click()
    await settle()

    const status = document.querySelector('[role="status"]')
    expect(status?.textContent).toContain('Generating summary…')

    i18n.global.locale.value = 'de'
    await nextTick()
    expect(status?.textContent).toContain('Zusammenfassung wird erstellt…')

    resolvePending?.('Fertig')
    await settle()
    expect(document.body.textContent).toContain('Fertig')
  })

  it('updates generic error message on locale switch while active, keeping Error.message intact', async () => {
    const { i18n } = mount(async () => {
      throw 'raw string error'
    }, 'en')
    await settle()
    button('Generate summary')?.click()
    await settle()

    const alert = document.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Something went wrong.')

    i18n.global.locale.value = 'de'
    await nextTick()
    expect(alert?.textContent).toContain('Etwas ist schiefgelaufen.')

    // Error instance retains message across locale switch (supplied data)
    const errorMount = mount(async () => {
      throw new Error('Explicit server error')
    }, 'en')
    await settle()
    const genButtons = document.querySelectorAll('button')
    const lastGen = [...genButtons].find(
      (b) => b.textContent?.trim() === 'Generate summary',
    )
    lastGen?.click()
    await settle()

    const specificAlert = document.querySelectorAll('[role="alert"]')
    const lastAlert = specificAlert[specificAlert.length - 1]
    expect(lastAlert?.textContent).toContain('Explicit server error')

    errorMount.i18n.global.locale.value = 'de'
    await nextTick()
    expect(lastAlert?.textContent).toContain('Explicit server error')
  })
})
