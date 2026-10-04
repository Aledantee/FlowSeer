// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h } from 'vue'
import UiAiLabel, { type UiAiLabelProps } from './UiAiLabel.vue'
import { createAiRegistry } from '../../ai'
import { createWebI18n } from '../../i18n'
import type { AiRun } from '../../ai'

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function mountLabel(props: UiAiLabelProps) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n('en')

  const app = createApp({
    render: () => h(UiAiLabel, props),
  })
  app.use(i18n)
  app.mount(host)
  disposers.push(() => app.unmount())
  return { host }
}

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 30))
}

describe('UiAiLabel', () => {
  it('shows AiRun.request context after the live target changed, and makes zero handler calls', async () => {
    const registry = createAiRegistry({
      viewport: () => ({ wide: true, narrow: false }),
    })
    const handler = vi.fn().mockResolvedValue({
      type: 'answer',
      text: 'Mock response',
    })
    registry.onRequest(handler)

    const el = document.createElement('div')
    document.body.append(el)
    const initialTarget = {
      id: 'target-1',
      kind: 'chart',
      label: 'Traffic Chart',
      context: { peak: '410 Mbps at 14:00' },
    }
    registry.register(el, initialTarget)

    const run: AiRun = registry.request(initialTarget, { action: 'summary' })

    // Simulate handler called once for the request above
    expect(handler).toHaveBeenCalledTimes(1)
    handler.mockClear()

    // Live target changes its context on screen
    registry.register(el, {
      ...initialTarget,
      context: { peak: '890 Mbps at 16:00' },
    })

    // Mount UiAiLabel with defaultOpen=true
    mountLabel({ run, defaultOpen: true })
    await settle()

    // Popover content is portalled to document.body
    expect(document.body.textContent).toContain('Traffic Chart')
    expect(document.body.textContent).toContain('peak:')
    expect(document.body.textContent).toContain('410 Mbps at 14:00')
    expect(document.body.textContent).not.toContain('890 Mbps at 16:00')
    expect(document.body.textContent).toContain(
      'AI output can be wrong; check the linked items.',
    )

    // Verify zero handler calls made by UiAiLabel
    expect(handler).not.toHaveBeenCalled()
  })

  it('renders sources when sources prop is provided', async () => {
    const run: AiRun = {
      requestId: 'req-123',
      request: {
        requestId: 'req-123',
        action: 'summary',
        history: [],
        targets: [
          {
            id: 'd1',
            kind: 'device',
            view: 'devices',
            label: 'Edge Switch',
            context: {},
          },
        ],
      },
      snapshots: (async function* () {})(),
      stop: vi.fn(),
    }

    mountLabel({
      run,
      sources: [
        { kind: 'device', id: 'd2', label: 'Upstream Core' },
        'SNMP MIB-II',
      ],
      defaultOpen: true,
    })
    await settle()

    expect(document.body.textContent).toContain('Upstream Core')
    expect(document.body.textContent).toContain('SNMP MIB-II')
  })
})
