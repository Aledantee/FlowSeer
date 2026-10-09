// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import UiAiAssistant from './UiAiAssistant.vue'
import {
  createAiRegistry,
  type AiRegistry,
  type AiRequest,
  type AiSeed,
  type AiTarget,
  type AiTargetSnapshot,
} from '../../ai'
import { aiRegistryKey } from './context'
import { createWebI18n } from '../../i18n'

describe('UiAiAssistant', () => {
  let app: App | null = null
  let container: HTMLDivElement | null = null
  let registry: AiRegistry

  beforeEach(() => {
    registry = createAiRegistry()
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    if (app) {
      app.unmount()
      app = null
    }
    container?.remove()
    container = null
    document.body.innerHTML = ''
  })

  function mountAssistant(props: Record<string, unknown> = {}) {
    const i18n = createWebI18n()
    const el = document.createElement('div')
    container?.appendChild(el)
    app = createApp({
      render() {
        return h(UiAiAssistant, props)
      },
    })
    app.use(i18n)
    app.provide(aiRegistryKey, registry)
    app.mount(el)
    return container!
  }

  it('shows the chip and the first turn on seeded open', async () => {
    const seed: AiSeed = {
      targets: [
        {
          id: 'device-1',
          kind: 'device',
          view: 'devices',
          label: 'core-sw-1',
          context: { health: 'Offline' },
        },
      ],
      turns: [
        { role: 'user', prompt: 'Why is this offline?' },
        {
          role: 'assistant',
          result: {
            type: 'summary',
            tone: 'critical',
            headline: 'core-sw-1 is offline',
            findings: [
              { severity: 'critical', title: 'Power supply fault', refs: [] },
            ],
            metrics: [],
            next: [],
            sources: [],
          },
        },
      ],
    }

    const root = mountAssistant({ seed })
    await nextTick()

    const chips = root.querySelector('[data-ai-assistant-chips]')
    expect(chips?.textContent).toContain('core-sw-1')

    const userTurn = root.querySelector('[data-ai-assistant-user-turn]')
    expect(userTurn?.textContent).toContain('Why is this offline?')

    const assistantTurn = root.querySelector('[data-ai-assistant-turn]')
    expect(assistantTurn?.textContent).toContain('core-sw-1 is offline')
    expect(assistantTurn?.textContent).toContain('Power supply fault')
  })

  it('carries targets and two-entry history in follow-up, succeeding after seeded target unregisters', async () => {
    const devEl = document.createElement('div')
    document.body.appendChild(devEl)
    const target: AiTarget = {
      id: 'device-1',
      kind: 'device',
      view: 'devices',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }
    registry.register(devEl, target)

    const seed: AiSeed = {
      targets: [
        {
          id: target.id,
          kind: target.kind,
          view: target.view!,
          label: target.label,
          context: target.context,
        },
      ],
      turns: [
        { role: 'user', prompt: 'Why is this offline?' },
        {
          role: 'assistant',
          result: {
            type: 'answer',
            text: 'Initial answer',
            refs: [],
          },
        },
      ],
    }

    let capturedRequest: AiRequest | undefined
    registry.onRequest(async (req) => {
      capturedRequest = req
      return {
        type: 'answer',
        text: 'Follow-up answer about ports',
        refs: [],
      }
    })

    const root = mountAssistant({ seed })
    await nextTick()

    registry.unregister(devEl)

    const textarea = root.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea).toBeTruthy()
    textarea.value = 'What about the ports?'
    textarea.dispatchEvent(new Event('input'))
    await nextTick()

    textarea.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(capturedRequest).toBeDefined()
    expect(capturedRequest?.prompt).toBe('What about the ports?')
    expect(capturedRequest?.targets).toHaveLength(1)
    expect(capturedRequest?.targets[0]?.id).toBe('device-1')
    expect(capturedRequest?.history).toHaveLength(2)
    expect(capturedRequest?.history[0]?.role).toBe('user')
    expect(capturedRequest?.history[1]?.role).toBe('assistant')

    expect(root.textContent).toContain('Follow-up answer about ports')
  })

  it('removes a chip and drops it from the next request', async () => {
    let capturedRequest: AiRequest | undefined
    registry.onRequest(async (req) => {
      capturedRequest = req
      return { type: 'answer', text: 'Ok', refs: [] }
    })

    const seed: AiSeed = {
      targets: [
        {
          id: 'd1',
          kind: 'device',
          view: 'devices',
          label: 'core-sw-1',
          context: {},
        },
        {
          id: 's1',
          kind: 'site',
          view: 'dashboard',
          label: 'Berlin Mitte',
          context: {},
        },
      ],
      turns: [],
    }

    const root = mountAssistant({ seed })
    await nextTick()

    const chipRow = root.querySelector('[data-ai-assistant-chips]')
    expect(chipRow?.textContent).toContain('core-sw-1')
    expect(chipRow?.textContent).toContain('Berlin Mitte')

    const removeButtons = chipRow?.querySelectorAll('button[aria-label]')
    expect(removeButtons?.length).toBeGreaterThan(0)
    ;(removeButtons![0] as HTMLButtonElement).click()
    await nextTick()

    expect(chipRow?.textContent).not.toContain('core-sw-1')
    expect(chipRow?.textContent).toContain('Berlin Mitte')

    const textarea = root.querySelector('textarea') as HTMLTextAreaElement
    textarea.value = 'Check status'
    textarea.dispatchEvent(new Event('input'))
    await nextTick()

    textarea.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(capturedRequest?.targets).toHaveLength(1)
    expect(capturedRequest?.targets[0]?.id).toBe('s1')
  })

  it('appends a chip when selecting a target from Add context', async () => {
    const devEl = document.createElement('div')
    document.body.appendChild(devEl)
    registry.register(devEl, {
      id: 'device-2',
      kind: 'device',
      label: 'dist-sw-1',
      context: {},
    })

    const root = mountAssistant({ seed: { targets: [], turns: [] } })
    await nextTick()

    const addContextBtn = root.querySelector(
      '[data-ai-assistant-add-context]',
    ) as HTMLButtonElement
    expect(addContextBtn).toBeTruthy()
    addContextBtn.click()
    await nextTick()

    const commandItem =
      (document.body.querySelector(
        '[data-reka-collection-item]',
      ) as HTMLElement) ??
      (document.body.querySelector('[data-command-value]') as HTMLElement)
    expect(commandItem?.textContent).toContain('dist-sw-1')
    commandItem.click()
    await nextTick()

    const chipRow = root.querySelector('[data-ai-assistant-chips]')
    expect(chipRow?.textContent).toContain('dist-sw-1')
  })

  it('updates suggestions to follow the chips, and hides them when thread is non-empty', async () => {
    const offlineDevice: AiTargetSnapshot = {
      id: 'd1',
      kind: 'device',
      view: 'devices',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    const root = mountAssistant({
      seed: { targets: [offlineDevice], turns: [] },
    })
    await nextTick()

    expect(root.textContent).toContain('Why is this offline?')
    expect(root.textContent).toContain('Summarize this device')

    const suggestionBtn = root.querySelector(
      '[data-ai-assistant-suggestion]',
    ) as HTMLButtonElement
    expect(suggestionBtn).toBeTruthy()
    expect(suggestionBtn.textContent?.trim()).toBe('Why is this offline?')

    registry.onRequest(async () => ({
      type: 'answer',
      text: 'Power was cut.',
      refs: [],
    }))

    suggestionBtn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 50))
    await nextTick()

    expect(root.querySelector('[data-ai-assistant-suggestion]')).toBeNull()
    expect(root.textContent).toContain('Why is this offline?')
    expect(root.textContent).toContain('Power was cut.')
  })

  it('Enter sends prompt while Shift+Enter inserts newline', async () => {
    let promptSent: string | undefined
    registry.onRequest(async (req) => {
      promptSent = req.prompt
      return { type: 'answer', text: 'Answer', refs: [] }
    })

    const root = mountAssistant({ seed: { targets: [], turns: [] } })
    await nextTick()

    const textarea = root.querySelector('textarea') as HTMLTextAreaElement
    textarea.value = 'First line'
    textarea.dispatchEvent(new Event('input'))
    await nextTick()

    const shiftEnterEvent = new KeyboardEvent('keydown', {
      key: 'Enter',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    })
    textarea.dispatchEvent(shiftEnterEvent)
    await nextTick()

    expect(promptSent).toBeUndefined()
    expect(shiftEnterEvent.defaultPrevented).toBe(false)

    const enterEvent = new KeyboardEvent('keydown', {
      key: 'Enter',
      shiftKey: false,
      bubbles: true,
      cancelable: true,
    })
    textarea.dispatchEvent(enterEvent)
    await nextTick()

    expect(enterEvent.defaultPrevented).toBe(true)
    await new Promise((r) => setTimeout(r, 50))
    await nextTick()

    expect(promptSent).toBe('First line')
  })

  it('replaces Send with Stop while run is pending, and aborts on stop', async () => {
    let aborted = false
    registry.onRequest(async function* (req) {
      yield {
        type: 'answer',
        text: 'Partial answer',
        refs: [],
      }
      await new Promise((resolve) => {
        if (req.signal.aborted) {
          aborted = true
          resolve(undefined)
          return
        }
        req.signal.addEventListener('abort', () => {
          aborted = true
          resolve(undefined)
        })
      })
    })

    const root = mountAssistant({ seed: { targets: [], turns: [] } })
    await nextTick()

    const textarea = root.querySelector('textarea') as HTMLTextAreaElement
    textarea.value = 'Run forever'
    textarea.dispatchEvent(new Event('input'))
    await nextTick()

    const sendBtn = root.querySelector(
      '[data-ai-assistant-send]',
    ) as HTMLButtonElement
    sendBtn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    await nextTick()

    const stopBtn = root.querySelector(
      '[data-ai-assistant-stop]',
    ) as HTMLButtonElement
    expect(stopBtn).toBeTruthy()
    expect(root.querySelector('[data-ai-assistant-send]')).toBeNull()

    stopBtn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    await nextTick()

    expect(aborted).toBe(true)
    expect(root.textContent).toContain('Stopped')
    expect(root.querySelector('[data-ai-assistant-send]')).toBeTruthy()
  })

  it('shows unavailable state when registry has no handler', async () => {
    const root = mountAssistant({ seed: { targets: [], turns: [] } })
    await nextTick()

    const textarea = root.querySelector('textarea') as HTMLTextAreaElement
    textarea.value = 'Test question'
    textarea.dispatchEvent(new Event('input'))
    await nextTick()

    const sendBtn = root.querySelector(
      '[data-ai-assistant-send]',
    ) as HTMLButtonElement
    sendBtn.click()
    await nextTick()

    expect(root.textContent).toContain('AI is unavailable')
  })

  it('emits close and update:open false when close button clicked', async () => {
    let closed = false
    let openVal = true
    const root = mountAssistant({
      open: true,
      onClose: () => {
        closed = true
      },
      'onUpdate:open': (val: boolean) => {
        openVal = val
      },
    })
    await nextTick()

    const closeBtn = root.querySelector('header button') as HTMLButtonElement
    closeBtn.click()
    await nextTick()

    expect(closed).toBe(true)
    expect(openVal).toBe(false)
  })

  it('renders in docked and narrow presentations with Signal styling', async () => {
    const seed: AiSeed = {
      targets: [
        {
          id: 'device-1',
          kind: 'device',
          view: 'devices',
          label: 'edge-gw-01',
          context: { health: 'Healthy' },
        },
      ],
      turns: [
        { role: 'user', prompt: 'Status report' },
        {
          role: 'assistant',
          result: {
            type: 'answer',
            text: 'System operating within nominal thresholds.',
            refs: [],
          },
        },
      ],
    }

    // Docked container
    const dockedHost = document.createElement('div')
    dockedHost.className = 'w-[420px] h-[700px]'
    container?.appendChild(dockedHost)
    const appDocked = createApp({
      render() {
        return h(UiAiAssistant, { seed })
      },
    })
    appDocked.use(createWebI18n())
    appDocked.provide(aiRegistryKey, registry)
    appDocked.mount(dockedHost)
    await nextTick()

    const titleEl = dockedHost.querySelector('header h2')
    expect(titleEl?.className).toContain('font-mono')
    expect(titleEl?.className).toContain('uppercase')

    const assistantTurn = dockedHost.querySelector('[data-ai-assistant-turn]')
    expect(assistantTurn?.classList.contains('ui-card')).toBe(true)

    appDocked.unmount()
    dockedHost.remove()

    // Narrow container
    const narrowHost = document.createElement('div')
    narrowHost.className = 'w-72 h-[500px]'
    container?.appendChild(narrowHost)
    const appNarrow = createApp({
      render() {
        return h(UiAiAssistant, { seed })
      },
    })
    appNarrow.use(createWebI18n())
    appNarrow.provide(aiRegistryKey, registry)
    appNarrow.mount(narrowHost)
    await nextTick()

    expect(narrowHost.querySelector('[data-ai-assistant]')).toBeTruthy()
    expect(narrowHost.querySelector('[data-ai-assistant-chips]')).toBeTruthy()
    appNarrow.unmount()
    narrowHost.remove()
  })

  it('allows contextual target selection to add a target to context chips', async () => {
    const target1: AiTarget = {
      id: 'target-sw-1',
      kind: 'device',
      view: 'devices',
      label: 'Switch 1',
      context: { role: 'switch' },
    }
    const target2: AiTarget = {
      id: 'target-sw-2',
      kind: 'device',
      view: 'devices',
      label: 'Switch 2',
      context: { role: 'switch' },
    }
    const devEl1 = document.createElement('div')
    const devEl2 = document.createElement('div')
    document.body.appendChild(devEl1)
    document.body.appendChild(devEl2)
    registry.register(devEl1, target1)
    registry.register(devEl2, target2)

    const seed: AiSeed = {
      targets: [
        {
          id: target1.id,
          kind: target1.kind,
          view: target1.view!,
          label: target1.label,
          context: target1.context,
        },
      ],
      turns: [],
    }

    const root = mountAssistant({ seed })
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const addBtn = root.querySelector(
      '[data-ai-assistant-add-context]',
    ) as HTMLButtonElement
    expect(addBtn).toBeTruthy()
    addBtn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Switch 2 should be in available targets, but Switch 1 should be filtered out
    const items = Array.from(document.body.querySelectorAll('[role="option"]'))
    const switch2Option = items.find((el) => el.textContent?.includes('Switch 2'))
    expect(switch2Option).toBeDefined()
    const switch1Option = items.find((el) => el.textContent?.includes('Switch 1'))
    expect(switch1Option).toBeUndefined()

    // Selecting Switch 2 adds it to chips
    switch2Option?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const chips = root.querySelector('[data-ai-assistant-chips]')
    expect(chips?.textContent).toContain('Switch 1')
    expect(chips?.textContent).toContain('Switch 2')

    devEl1.remove()
    devEl2.remove()
  })

  it('renders loading skeleton then transitions to error state on failure', async () => {
    let rejectPromise: ((err: Error) => void) | null = null
    registry.onRequest(async () => {
      return new Promise<never>((_, reject) => {
        rejectPromise = reject
      })
    })

    const root = mountAssistant({ seed: { targets: [], turns: [] } })
    await nextTick()

    const textarea = root.querySelector('textarea') as HTMLTextAreaElement
    textarea.value = 'Trigger error flow'
    textarea.dispatchEvent(new Event('input'))
    await nextTick()

    const sendBtn = root.querySelector(
      '[data-ai-assistant-send]',
    ) as HTMLButtonElement
    sendBtn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // During loading: skeleton is rendered
    expect(root.querySelector('[data-ai-result-skeleton]')).toBeTruthy()

    // Trigger failure
    expect(rejectPromise).not.toBeNull()
    rejectPromise!(new Error('Backend model unavailable'))
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    await nextTick()

    // After failure: error message is rendered
    const errorEl = root.querySelector('[data-ai-result-error]')
    expect(errorEl).toBeTruthy()
    expect(errorEl?.textContent).toContain('Backend model unavailable')
  })
})
