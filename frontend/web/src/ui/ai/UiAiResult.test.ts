// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiAiResult, { type UiAiResultProps } from './UiAiResult.vue'
import { createWebI18n } from '../../i18n'
import type { AiAnswer, AiResult, AiRun, AiSummary, AiTone } from '../../ai'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function mountResult(
  props: UiAiResultProps,
  options?: { onStop?: () => void },
) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n('en')

  const app = createApp({
    render: () =>
      h(UiAiResult, {
        ...props,
        onStop: options?.onStop,
      }),
  })
  app.use(i18n)
  app.mount(host)
  disposers.push(() => app.unmount())
  return { host }
}

describe('UiAiResult', () => {
  it('renders every field of AiSummary and displays cause confidence and metrics dl', () => {
    const summary: AiSummary = {
      type: 'summary',
      tone: 'warning',
      headline: 'Berlin Distribution Switch has high packet drop',
      findings: [
        {
          title: 'Port ge-0/0/1 flapping',
          severity: 'warning',
          refs: [{ kind: 'device', id: 'd-dist-1', label: 'dist-sw-1' }],
        },
      ],
      cause: {
        text: 'Optical transceiver failure',
        confidence: 'medium',
        refs: [{ kind: 'device', id: 'd-core-1', label: 'core-sw-1' }],
      },
      impact: {
        text: 'Backup uplink took over traffic with 30ms latency increase',
        refs: [],
      },
      metrics: [
        { label: 'DropRate', value: '12.4%' },
        { label: 'LinkFlaps', value: '42' },
      ],
      next: [
        { label: 'Replace SFP module on ge-0/0/1' },
        { label: 'Verify CRC counter' },
      ],
      sources: [{ kind: 'site', id: 'site-berlin', label: 'Berlin Mitte' }],
    }

    const { host } = mountResult({
      result: summary,
      state: 'done',
    })

    expect(
      host.querySelector('[data-ai-result-headline]')?.textContent,
    ).toContain('Berlin Distribution Switch has high packet drop')
    expect(host.querySelector('[data-ai-result-tone]')?.textContent).toContain(
      'Needs attention',
    )
    expect(
      host.querySelector('[data-ai-finding-severity]')?.textContent,
    ).toContain('warning')
    expect(host.textContent).toContain('Port ge-0/0/1 flapping')
    expect(host.querySelector('[data-ai-cause-header]')?.textContent).toBe(
      'Likely cause · medium confidence',
    )
    expect(host.textContent).toContain('Optical transceiver failure')
    expect(
      host.querySelector('[data-ai-result-impact]')?.textContent,
    ).toContain('Backup uplink took over traffic with 30ms latency increase')
    const dl = host.querySelector('[data-ai-result-metrics] dl')
    expect(dl).not.toBeNull()
    expect(dl?.textContent).toContain('DropRate')
    expect(dl?.textContent).toContain('12.4%')
    expect(dl?.textContent).toContain('LinkFlaps')
    expect(dl?.textContent).toContain('42')
    expect(
      host.querySelector('[data-ai-result-next-steps]')?.textContent,
    ).toContain('Replace SFP module on ge-0/0/1')
    expect(host.textContent).toContain('Berlin Mitte')
  })

  it('omits empty sections from the DOM', () => {
    const minimalSummary: AiSummary = {
      type: 'summary',
      headline: 'All systems normal.',
      tone: 'ok',
      findings: [],
      metrics: [],
      next: [],
      sources: [],
    }

    const { host } = mountResult({
      result: minimalSummary,
      state: 'done',
    })

    expect(host.querySelector('[data-ai-result-headline]')).not.toBeNull()
    expect(host.querySelector('[data-ai-result-findings]')).toBeNull()
    expect(host.querySelector('[data-ai-result-cause]')).toBeNull()
    expect(host.querySelector('[data-ai-result-impact]')).toBeNull()
    expect(host.querySelector('[data-ai-result-metrics]')).toBeNull()
    expect(host.querySelector('[data-ai-result-next-steps]')).toBeNull()
    expect(host.querySelector('[data-ai-result-refs]')).toBeNull()
  })

  it('renders an answer tree after its text and entity chips', () => {
    const answer: AiAnswer = {
      type: 'answer',
      text: 'Two switches are offline.',
      refs: [{ kind: 'device', id: 'core-sw-1', label: 'Core switch' }],
      ui: [
        {
          component: 'UiCard',
          props: {},
          children: [
            { component: 'UiStatusBadge', props: { status: 'Offline' } },
            {
              component: 'UiButton',
              props: {
                text: 'Open device',
                intent: {
                  type: 'navigate',
                  target: { path: '/devices/core-sw-1' },
                },
              },
            },
          ],
        },
      ],
    }

    const { host } = mountResult({ result: answer, state: 'done' })
    const text = host.querySelector('[data-ai-result-text]')
    const refs = host.querySelector('[data-ai-result-refs]')
    const render = host.querySelector('[data-ai-render]')

    expect(text?.textContent).toContain('Two switches are offline.')
    expect(refs).not.toBeNull()
    expect(render).not.toBeNull()
    expect(text?.compareDocumentPosition(render as Node)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    )
    expect(refs?.compareDocumentPosition(render as Node)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    )
    expect(render?.textContent).toContain('Offline')
  })

  it('leaves answer text standing when its tree is rejected', () => {
    const answer: AiAnswer = {
      type: 'answer',
      text: 'Two switches are offline.',
      refs: [],
      ui: [{ component: 'div', props: {} }],
    }

    const { host } = mountResult({ result: answer, state: 'done' })

    expect(host.querySelector('[data-ai-result-text]')?.textContent).toContain(
      'Two switches are offline.',
    )
    expect(host.querySelector('[data-ai-render] [role="alert"]')).not.toBeNull()
    expect(host.querySelector('[data-ai-result-error]')).toBeNull()
  })

  it('does not mount the tree renderer when an answer has no ui', () => {
    const answer: AiAnswer = {
      type: 'answer',
      text: 'Everything is healthy.',
      refs: [],
    }

    const { host } = mountResult({ result: answer, state: 'done' })

    expect(host.textContent).toContain('Everything is healthy.')
    expect(host.querySelector('[data-ai-render]')).toBeNull()
  })

  it('maps tone to the required badge variants and labels', () => {
    const tones: { tone: AiTone; expectedLabel: string }[] = [
      { tone: 'ok', expectedLabel: 'Healthy' },
      { tone: 'warning', expectedLabel: 'Needs attention' },
      { tone: 'critical', expectedLabel: 'Critical' },
      { tone: 'unknown', expectedLabel: 'Unknown' },
    ]

    for (const { tone, expectedLabel } of tones) {
      const { host } = mountResult({
        result: {
          type: 'summary',
          headline: 'Test',
          tone,
          findings: [],
          metrics: [],
          next: [],
          sources: [],
        },
        state: 'done',
      })
      const badge = host.querySelector('[data-ai-result-tone]')
      expect(badge?.textContent?.trim()).toBe(expectedLabel)
    }
  })

  it('renders shimmer skeleton in generating state before snapshots arrive', () => {
    const { host } = mountResult({
      result: null,
      state: 'generating',
      showStop: true,
    })

    const root = host.querySelector('[data-ai-result]')
    expect(root?.getAttribute('aria-busy')).toBe('true')
    expect(host.querySelector('[data-ai-result-skeleton]')).not.toBeNull()
    expect(host.querySelectorAll('.ai-shimmer').length).toBeGreaterThan(0)
    expect(host.textContent).toContain('Stop')
  })

  it('renders Stopped state with last snapshot intact', () => {
    const summary: AiSummary = {
      type: 'summary',
      headline: 'Partial summary before cancel',
      tone: 'ok',
      findings: [],
      metrics: [],
      next: [],
      sources: [],
    }

    const { host } = mountResult({
      result: summary,
      state: 'stopped',
    })

    expect(host.querySelector('[data-ai-result-stopped]')).not.toBeNull()
    expect(host.textContent).toContain('Stopped')
    expect(host.textContent).toContain('Partial summary before cancel')
  })

  it('renders error state with role alert', () => {
    const { host } = mountResult({
      state: 'error',
      error: 'The AI returned a result FlowSeer cannot show.',
    })

    const alert = host.querySelector('[role="alert"]')
    expect(alert).not.toBeNull()
    expect(alert?.textContent).toContain(
      'The AI returned a result FlowSeer cannot show.',
    )
  })

  it('announces exactly one polite status message per state', async () => {
    const announcements: string[] = []

    const states: UiAiResultProps['state'][] = [
      'generating',
      'done',
      'stopped',
      'error',
    ]

    for (const st of states) {
      const { host } = mountResult({ state: st })
      const statusEl = host.querySelector('[data-ai-announcement]')
      expect(statusEl?.getAttribute('role')).toBe('status')
      expect(statusEl?.getAttribute('aria-live')).toBe('polite')
      announcements.push(statusEl?.textContent?.trim() ?? '')
    }

    expect(announcements[0]).toBe('Generating summary…')
    expect(announcements[1]).toBe('Summary complete')
    expect(announcements[2]).toBe('Generation stopped')
    expect(announcements[3]).toBe('Error generating summary')
  })

  it('drives snapshots from an AiRun and handles Stop', async () => {
    const secondPromise = new Promise<void>(() => {})

    async function* generateSnapshots() {
      yield {
        type: 'summary',
        headline: 'Snapshot 1',
        tone: 'ok',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      } as AiResult
      await secondPromise
      yield {
        type: 'summary',
        headline: 'Snapshot 2',
        tone: 'ok',
        findings: [],
        metrics: [],
        next: [],
        sources: [],
      } as AiResult
    }

    const run: AiRun = {
      requestId: 'test-run-1',
      request: {
        requestId: 'test-run-1',
        action: 'summary',
        history: [],
        targets: [],
      },
      snapshots: generateSnapshots(),
      stop: vi.fn(),
    }

    const { host } = mountResult({ run, showStop: true })
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(host.textContent).toContain('Snapshot 1')

    const stopBtn = host.querySelector('button')
    stopBtn?.click()
    await nextTick()

    expect(run.stop).toHaveBeenCalled()
    expect(host.querySelector('[data-ai-result-stopped]')).not.toBeNull()
    expect(host.textContent).toContain('Snapshot 1')
  })
})
