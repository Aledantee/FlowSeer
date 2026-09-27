// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, nextTick } from 'vue'
import UiAiActionLayer from './UiAiActionLayer.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import type { AiRegistry } from '../../ai'
import type { AiRequest, AiTarget } from '../../ai'

let dispose = () => {}
afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
})

interface Box {
  top: number
  left: number
  width: number
  height: number
}

function setBox(element: HTMLElement, box: Box) {
  element.getBoundingClientRect = () =>
    ({
      x: box.left,
      y: box.top,
      width: box.width,
      height: box.height,
      top: box.top,
      left: box.left,
      right: box.left + box.width,
      bottom: box.top + box.height,
      toJSON: () => ({}),
    }) as DOMRect
}

const box: Box = { top: 120, left: 60, width: 320, height: 40 }

function setup(boxValue: Box = box) {
  const registry: AiRegistry = createAiRegistry({
    viewport: () => ({ wide: true, narrow: false }),
  })
  const element = document.createElement('div')
  element.tabIndex = 0
  setBox(element, boxValue)
  document.body.append(element)
  const target: AiTarget = {
    id: 'a:devices:device:d1',
    kind: 'device',
    label: 'd1',
    context: { site: 'Berlin Mitte' },
  }
  registry.register(element, target)

  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp(UiAiActionLayer)
  app.provide(aiRegistryKey, registry)
  app.mount(host)
  dispose = () => app.unmount()
  return { registry, element, target, host }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 0))
  await nextTick()
}

function askButton(): HTMLButtonElement | undefined {
  return document.querySelector<HTMLButtonElement>('.ai-ask') ?? undefined
}

function type(text: string) {
  const textarea = document.querySelector('textarea')
  if (!textarea) throw new Error('Missing prompt field')
  textarea.value = text
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
  return textarea
}

function submitButton(): HTMLButtonElement {
  const button = document.querySelector<HTMLButtonElement>(
    'form button[type="submit"]',
  )
  if (!button) throw new Error('Missing ask submit')
  return button
}

describe('AiActionLayer selection and Ask', () => {
  it('reveals Ask for the selected target and hovers it in place', async () => {
    const { registry } = setup()
    registry.highlight('a:devices:device:d1')
    await settle()

    const button = askButton()
    expect(button).not.toBeNull()
    expect(button?.getAttribute('aria-label')).toBe('Ask about d1')
    expect(button?.style.top).toBe('124px')
    expect(document.querySelector('.ai-outline')).not.toBeNull()
  })

  it('submits only a nonempty prompt and shows the answer', async () => {
    const { registry } = setup()
    const seen: AiRequest[] = []
    registry.onRequest((request) => {
      seen.push(request)
      return 'It stopped answering its last poll.'
    })
    registry.highlight('a:devices:device:d1')
    await settle()

    askButton()?.click()
    await settle()

    type('   ')
    await settle()
    expect(submitButton().disabled).toBe(true)
    expect(seen).toHaveLength(0)

    type('Why offline?')
    await settle()
    submitButton().click()
    await settle()

    expect(seen).toHaveLength(1)
    expect(seen[0]?.kind).toBe('ask')
    expect(seen[0]?.prompt).toBe('Why offline?')
    expect(seen[0]?.context).toEqual({ site: 'Berlin Mitte' })
    expect(document.body.textContent).toContain(
      'It stopped answering its last poll.',
    )
  })

  it('reports the unavailable state with no handler', async () => {
    const { registry } = setup()
    registry.highlight('a:devices:device:d1')
    await settle()
    askButton()?.click()
    await settle()
    type('Why offline?')
    await settle()
    submitButton().click()
    await settle()

    expect(document.body.textContent).toContain('AI is unavailable')
  })

  it('shows an error when the handler rejects', async () => {
    const { registry } = setup()
    registry.onRequest(async () => {
      throw new Error('The model is unreachable.')
    })
    registry.highlight('a:devices:device:d1')
    await settle()
    askButton()?.click()
    await settle()
    type('Why offline?')
    await settle()
    submitButton().click()
    await settle()

    const alert = document.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('The model is unreachable.')
  })

  it('discards an answer whose target unmounted before it resolved', async () => {
    const { registry, element } = setup()
    let release: ((value: string) => void) | undefined
    registry.onRequest(
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )
    registry.highlight('a:devices:device:d1')
    await settle()
    askButton()?.click()
    await settle()
    type('Why offline?')
    await settle()
    submitButton().click()
    await settle()

    registry.unregister(element)
    await settle()
    release?.('A late answer')
    await settle()

    expect(document.body.textContent).not.toContain('A late answer')
  })

  it('opens Ask from the focused target with Alt+A', async () => {
    const { element } = setup()
    element.focus()
    await settle()

    expect(askButton()).not.toBeNull()
    window.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'a',
        code: 'KeyA',
        altKey: true,
        bubbles: true,
        cancelable: true,
      }),
    )
    await settle()

    expect(document.querySelector('textarea')).not.toBeNull()
  })

  it('reveals Ask on pointer entry', async () => {
    const { element } = setup()
    element.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }))
    await settle()
    expect(askButton()).not.toBeNull()
  })
})

describe('AiActionLayer geometry', () => {
  it('hides the affordance when the target is scrolled out of view', async () => {
    const { registry } = setup({ top: -400, left: 60, width: 320, height: 40 })
    registry.highlight('a:devices:device:d1')
    await settle()

    expect(askButton()).toBeUndefined()
  })

  it('follows the element when it moves', async () => {
    const { registry, element } = setup()
    registry.highlight('a:devices:device:d1')
    await settle()
    expect(askButton()?.style.top).toBe('124px')

    setBox(element, { top: 300, left: 60, width: 320, height: 40 })
    window.dispatchEvent(new Event('resize'))
    await settle()

    expect(askButton()?.style.top).toBe('304px')
  })
})
