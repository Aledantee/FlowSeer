// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h } from 'vue'
import UiAiResultActions, {
  serializeAiResultToText,
  type UiAiResultActionsProps,
} from './UiAiResultActions.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import { createWebI18n } from '../../i18n'
import type { AiResult, AiSummary } from '../../ai'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function mountActions(
  props: UiAiResultActionsProps,
  options?: {
    onCopy?: (text: string) => void
    onRegenerate?: () => void
    onFeedback?: (p: { requestId: string; rating: 'up' | 'down' }) => void
    registry?: ReturnType<typeof createAiRegistry>
  },
) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n('en')
  const registry =
    options?.registry ??
    createAiRegistry({ viewport: () => ({ wide: true, narrow: false }) })

  const app = createApp({
    render: () =>
      h(UiAiResultActions, {
        ...props,
        onCopy: options?.onCopy,
        onRegenerate: options?.onRegenerate,
        onFeedback: options?.onFeedback,
      }),
  })
  app.use(i18n)
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  disposers.push(() => app.unmount())
  return { host, registry }
}

describe('UiAiResultActions', () => {
  it('serializes AiSummary into clear plain text for copy', () => {
    const summary: AiSummary = {
      type: 'summary',
      headline: 'Berlin Mitte Core Switch is degraded',
      tone: 'warning',
      findings: [
        {
          title: 'Interface port 3 CRC errors',
          severity: 'warning',
          refs: [],
        },
        { title: 'Power supply PSU2 unseated', severity: 'critical', refs: [] },
      ],
      cause: {
        text: 'Faulty fiber patch cable',
        confidence: 'medium',
        refs: [],
      },
      impact: {
        text: 'Secondary uplink offline',
        refs: [],
      },
      metrics: [
        { label: 'PacketLoss', value: '4.2%' },
        { label: 'CRC_Errors', value: '128' },
      ],
      next: [{ label: 'Inspect port 3 cable' }, { label: 'Reseat PSU2' }],
      sources: [],
    }

    const text = serializeAiResultToText(summary)
    expect(text).toContain('Berlin Mitte Core Switch is degraded')
    expect(text).toContain('Findings:\n- [warning] Interface port 3 CRC errors')
    expect(text).toContain('- [critical] Power supply PSU2 unseated')
    expect(text).toContain(
      'Likely cause (medium confidence): Faulty fiber patch cable',
    )
    expect(text).toContain('Impact: Secondary uplink offline')
    expect(text).toContain('Metrics:\n- PacketLoss: 4.2%\n- CRC_Errors: 128')
    expect(text).toContain(
      'Next steps:\n1. Inspect port 3 cable\n2. Reseat PSU2',
    )
  })

  it('serializes AiAnswer into text', () => {
    const answer: AiResult = {
      type: 'answer',
      text: 'Simple answer text here.',
      refs: [],
    }
    expect(serializeAiResultToText(answer)).toBe('Simple answer text here.')
  })

  it('copies text and emits copy event when Copy button is clicked', async () => {
    const onCopy = vi.fn()
    const result: AiResult = {
      type: 'answer',
      text: 'Sample answer to copy',
      refs: [],
    }

    const { host } = mountActions({ result, requestId: 'r1' }, { onCopy })
    const copyBtn = host.querySelector(
      '[data-action="copy"]',
    ) as HTMLButtonElement | null
    expect(copyBtn).not.toBeNull()

    copyBtn?.click()
    await new Promise((r) => setTimeout(r, 10))
    expect(onCopy).toHaveBeenCalledWith('Sample answer to copy')
  })

  it('emits regenerate when Regenerate button is clicked', () => {
    const onRegenerate = vi.fn()
    const { host } = mountActions({ requestId: 'r1' }, { onRegenerate })
    const regenBtn = host.querySelector(
      '[data-action="regenerate"]',
    ) as HTMLButtonElement | null
    expect(regenBtn).not.toBeNull()

    regenBtn?.click()
    expect(onRegenerate).toHaveBeenCalledTimes(1)
  })

  it('delivers feedback to registry and emits feedback payload on thumbs-down and thumbs-up', () => {
    const feedbackListener = vi.fn()
    const registry = createAiRegistry({
      viewport: () => ({ wide: true, narrow: false }),
    })
    registry.onFeedback(feedbackListener)

    const onFeedback = vi.fn()
    const { host } = mountActions({ requestId: 'r7' }, { registry, onFeedback })

    const thumbsDown = host.querySelector(
      '[data-action="thumbs-down"]',
    ) as HTMLButtonElement | null
    expect(thumbsDown).not.toBeNull()

    thumbsDown?.click()

    expect(feedbackListener).toHaveBeenCalledWith({
      requestId: 'r7',
      rating: 'down',
    })
    expect(onFeedback).toHaveBeenCalledWith({
      requestId: 'r7',
      rating: 'down',
    })

    const thumbsUp = host.querySelector(
      '[data-action="thumbs-up"]',
    ) as HTMLButtonElement | null
    thumbsUp?.click()

    expect(feedbackListener).toHaveBeenCalledWith({
      requestId: 'r7',
      rating: 'up',
    })
    expect(onFeedback).toHaveBeenCalledWith({
      requestId: 'r7',
      rating: 'up',
    })
  })
})
