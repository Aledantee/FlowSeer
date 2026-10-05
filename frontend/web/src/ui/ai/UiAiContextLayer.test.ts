// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiAiContextLayer from './UiAiContextLayer.vue'
import UiAiTarget from './UiAiTarget.vue'
import {
  createAiRegistry,
  type AiRegistry,
  type AiResult,
  type AiSeed,
  type AiTarget,
} from '../../ai'
import { aiRegistryKey } from './context'
import { createWebI18n } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

async function mountLayer(
  registry: AiRegistry,
  slots: Record<string, () => unknown>,
  onContinue?: (seed: AiSeed) => void,
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    setup() {
      return () =>
        h(
          UiAiContextLayer,
          {
            onContinue,
          },
          slots,
        )
    },
  })
  app.use(createWebI18n())
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  dispose = () => app.unmount()
  await nextTick()
  return host
}

describe('UiAiContextLayer', () => {
  let registry: AiRegistry

  beforeEach(() => {
    registry = createAiRegistry()
  })

  it('right-clicking a registered item opens its verbs and is default-prevented', async () => {
    const offlineDevice: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    const host = await mountLayer(registry, {
      default: () => [
        h(
          UiAiTarget,
          { id: 'row-1', ai: offlineDevice },
          { default: () => h('span', 'core-sw-1') },
        ),
      ],
    })

    const row = host.querySelector('#row-1') as HTMLElement

    const event = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      clientX: 50,
      clientY: 50,
    })
    row.dispatchEvent(event)

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(event.defaultPrevented).toBe(true)
    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()
    expect(menu?.textContent).toContain('Why is this offline?')
    expect(menu?.textContent).toContain('Summarize this device')
    expect(menu?.textContent).toContain('Ask about this…')
  })

  it('Requirement 1 non-targets keep native menu without prevention', async () => {
    const offlineDevice: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }
    const viewTarget: AiTarget = {
      id: 'view:dashboard',
      kind: 'view',
      view: 'dashboard',
      label: 'Dashboard',
      context: {},
    }

    const host = await mountLayer(registry, {
      default: () => [
        h('h1', { id: 'heading' }, 'Overview Heading'),
        h(
          UiAiTarget,
          { id: 'row-1', ai: offlineDevice },
          {
            default: () => [
              h('a', { id: 'row-link', href: '/devices/d1' }, 'Link to device'),
              h('input', { id: 'row-input', type: 'text' }),
            ],
          },
        ),
        h(
          UiAiTarget,
          { id: 'view-pane', ai: viewTarget },
          {
            default: () => h('p', { id: 'pane-text' }, 'General pane content'),
          },
        ),
      ],
    })

    const heading = host.querySelector('#heading') as HTMLElement
    const link = host.querySelector('#row-link') as HTMLElement
    const input = host.querySelector('#row-input') as HTMLElement
    const paneText = host.querySelector('#pane-text') as HTMLElement

    // Heading (outside targets)
    const headingEvt = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
    })
    heading.dispatchEvent(headingEvt)
    await nextTick()
    expect(headingEvt.defaultPrevented).toBe(false)
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    // a[href] inside row
    const linkEvt = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
    })
    link.dispatchEvent(linkEvt)
    await nextTick()
    expect(linkEvt.defaultPrevented).toBe(false)
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    // input inside row
    const inputEvt = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
    })
    input.dispatchEvent(inputEvt)
    await nextTick()
    expect(inputEvt.defaultPrevented).toBe(false)
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    // View target point (kind: view)
    const viewEvt = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
    })
    paneText.dispatchEvent(viewEvt)
    await nextTick()
    expect(viewEvt.defaultPrevented).toBe(false)
    expect(document.body.querySelector('[role="menu"]')).toBeNull()
  })

  it('Shift+F10 on a focused row opens menu and prevents default; non-target does not', async () => {
    const target: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    const host = await mountLayer(registry, {
      default: () => [
        h(
          UiAiTarget,
          { id: 'row-1', tabindex: 0, ai: target },
          { default: () => 'Target row' },
        ),
        h('button', { id: 'outside-btn' }, 'Outside target'),
      ],
    })

    const row = host.querySelector('#row-1') as HTMLElement
    const outsideBtn = host.querySelector('#outside-btn') as HTMLElement

    // Shift+F10 on non-target
    outsideBtn.focus()
    const nonTargetEvt = new KeyboardEvent('keydown', {
      key: 'F10',
      code: 'F10',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    })
    outsideBtn.dispatchEvent(nonTargetEvt)
    await nextTick()
    expect(nonTargetEvt.defaultPrevented).toBe(false)
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    // Shift+F10 on focused row
    row.focus()
    const rowEvt = new KeyboardEvent('keydown', {
      key: 'F10',
      code: 'F10',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    })
    row.dispatchEvent(rowEvt)
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(rowEvt.defaultPrevented).toBe(true)
    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()
    expect(menu?.textContent).toContain('Why is this offline?')
  })

  it('verb -> pending -> result flow and focus order', async () => {
    const target: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    let resolveSnapshot: ((res: AiResult) => void) | undefined
    registry.onRequest(
      () =>
        new Promise((resolve) => {
          resolveSnapshot = resolve
        }),
    )

    const host = await mountLayer(registry, {
      default: () => [
        h(
          UiAiTarget,
          { as: 'button', id: 'row-btn', ai: target },
          { default: () => 'core-sw-1' },
        ),
      ],
    })

    const btn = host.querySelector('#row-btn') as HTMLElement
    btn.focus()

    // Open context menu
    btn.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()

    const item = [...document.body.querySelectorAll('[role="menuitem"]')].find(
      (el) => el.textContent?.includes('Why is this offline?'),
    ) as HTMLElement
    expect(item).toBeDefined()

    item.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Popover is now open and pending
    const popover = document.body.querySelector('[data-ai-context-popover]')
    expect(popover).not.toBeNull()
    const resultEl = popover?.querySelector('[data-ai-result]')
    expect(resultEl?.getAttribute('aria-busy')).toBe('true')

    // Resolve snapshot
    resolveSnapshot?.({
      type: 'summary',
      headline: 'Link flap detected',
      tone: 'critical',
      findings: [
        { severity: 'critical', title: 'Power supply fault', refs: [] },
      ],
      metrics: [],
      next: [],
      sources: [],
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(resultEl?.getAttribute('aria-busy')).toBe('false')
    expect(popover?.textContent).toContain('Link flap detected')
    expect(popover?.textContent).toContain('Power supply fault')
    expect(popover?.textContent).toContain('Continue in assistant')

    // Close popover via Escape -> should restore focus to origin element
    popover?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('[data-ai-context-popover]')).toBeNull()
    expect(document.activeElement).toBe(btn)
  })

  it('Ask about this… emits continue with target and empty turns', async () => {
    let continuedSeed: AiSeed | undefined
    const target: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    const host = await mountLayer(
      registry,
      {
        default: () => [
          h(
            UiAiTarget,
            { id: 'row-1', ai: target },
            { default: () => 'core-sw-1' },
          ),
        ],
      },
      (seed) => {
        continuedSeed = seed
      },
    )

    const row = host.querySelector('#row-1') as HTMLElement

    row.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const item = [...document.body.querySelectorAll('[role="menuitem"]')].find(
      (el) => el.textContent?.includes('Ask about this…'),
    ) as HTMLElement
    item.click()
    await nextTick()

    expect(continuedSeed).toBeDefined()
    expect(continuedSeed?.targets).toHaveLength(1)
    expect(continuedSeed?.targets[0]?.id).toBe('device:d1')
    expect(continuedSeed?.turns).toEqual([])
  })

  it('Continue in assistant button emits continue with user and assistant turns', async () => {
    let continuedSeed: AiSeed | undefined
    const target: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    registry.onRequest(async () => ({
      type: 'summary',
      headline: 'Offline diagnosis',
      tone: 'critical',
      findings: [],
      metrics: [],
      next: [],
      sources: [],
    }))

    const host = await mountLayer(
      registry,
      {
        default: () => [
          h(
            UiAiTarget,
            { id: 'row-1', ai: target },
            { default: () => 'core-sw-1' },
          ),
        ],
      },
      (seed) => {
        continuedSeed = seed
      },
    )

    const row = host.querySelector('#row-1') as HTMLElement

    row.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const item = [...document.body.querySelectorAll('[role="menuitem"]')].find(
      (el) => el.textContent?.includes('Why is this offline?'),
    ) as HTMLElement
    item.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 30))

    const continueBtn = document.body.querySelector(
      '[data-ai-continue]',
    ) as HTMLElement
    expect(continueBtn).not.toBeNull()
    continueBtn.click()
    await nextTick()

    expect(continuedSeed).toBeDefined()
    expect(continuedSeed?.targets[0]?.id).toBe('device:d1')
    expect(continuedSeed?.turns).toHaveLength(2)
    expect(continuedSeed?.turns[0]).toEqual({
      role: 'user',
      prompt: 'Why is this offline?',
    })
    expect(continuedSeed?.turns[1]?.role).toBe('assistant')
  })

  it('a context event on an SVG child path resolves the registered SVG root', async () => {
    const chart: AiTarget = {
      id: 'chart:t1',
      kind: 'chart',
      label: 'Traffic',
      context: {},
    }
    const host = await mountLayer(registry, {
      default: () => [
        h(
          UiAiTarget,
          { as: 'svg', id: 'chart', ai: chart },
          { default: () => h('path', { id: 'line', d: 'M0 0L10 10' }) },
        ),
      ],
    })
    const root = host.querySelector('#chart')
    const path = host.querySelector('#line')
    expect(root).toBeInstanceOf(SVGElement)
    expect(path?.parentElement).toBe(root)

    const event = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      clientX: 20,
      clientY: 20,
    })
    path?.dispatchEvent(event)
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(event.defaultPrevented).toBe(true)
    const menu = document.body.querySelector('[role="menu"]')
    expect(menu?.textContent).toContain('Explain this traffic')
  })

  it('runs a verb on an SVG target and anchors the result to it', async () => {
    const chart: AiTarget = {
      id: 'chart:t1',
      kind: 'chart',
      label: 'Traffic',
      context: {},
    }
    registry.onRequest(async () => ({
      type: 'answer',
      text: 'Traffic is steady.',
      refs: [],
    }))
    const host = await mountLayer(registry, {
      default: () => [
        h(
          UiAiTarget,
          { as: 'svg', id: 'chart', ai: chart },
          { default: () => h('path', { id: 'line', d: 'M0 0L10 10' }) },
        ),
      ],
    })

    host
      .querySelector('#line')
      ?.dispatchEvent(
        new MouseEvent('contextmenu', { bubbles: true, cancelable: true }),
      )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    const item = [...document.body.querySelectorAll('[role="menuitem"]')].find(
      (el) => el.textContent?.includes('Explain this traffic'),
    ) as HTMLElement
    item.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 30))

    const popover = document.body.querySelector('[data-ai-context-popover]')
    expect(popover?.textContent).toContain('Traffic is steady.')
  })

  it('target unmounting mid-run discards the result and closes popover', async () => {
    const target: AiTarget = {
      id: 'device:d1',
      kind: 'device',
      label: 'core-sw-1',
      context: { health: 'Offline' },
    }

    registry.onRequest(
      () =>
        new Promise(() => {
          // Never resolves
        }),
    )

    const host = await mountLayer(registry, {
      default: () => [
        h(
          UiAiTarget,
          { id: 'row-1', ai: target },
          { default: () => 'core-sw-1' },
        ),
      ],
    })

    const row = host.querySelector('#row-1') as HTMLElement

    row.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const item = [...document.body.querySelectorAll('[role="menuitem"]')].find(
      (el) => el.textContent?.includes('Why is this offline?'),
    ) as HTMLElement
    item.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(
      document.body.querySelector('[data-ai-context-popover]'),
    ).not.toBeNull()

    // Unregister target mid-run
    registry.unregister(row)
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('[data-ai-context-popover]')).toBeNull()
  })
})
