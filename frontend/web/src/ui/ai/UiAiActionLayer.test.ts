// @vitest-environment happy-dom
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import UiAiActionLayer from './UiAiActionLayer.vue'
import { aiRegistryKey } from './context'
import { createAiRegistry } from '../../ai'
import type { AiRegistry } from '../../ai'
import type { AiRequest, AiTarget } from '../../ai'

const appStyles = readFileSync(resolve(process.cwd(), 'src/style.css'), 'utf8')

let dispose = () => {}
afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  document.head.querySelector('[data-test-app-styles]')?.remove()
  vi.restoreAllMocks()
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

function boxesIntersect(first: Box, second: Box): boolean {
  return (
    first.left < second.left + second.width &&
    first.left + first.width > second.left &&
    first.top < second.top + second.height &&
    first.top + first.height > second.top
  )
}

function buttonBox(button: HTMLButtonElement | undefined): Box {
  if (!button) throw new Error('Missing Ask button')
  return {
    top: Number.parseFloat(button.style.top),
    left: Number.parseFloat(button.style.left),
    width: 26,
    height: 22,
  }
}

const box: Box = { top: 120, left: 60, width: 320, height: 40 }

function setup(
  boxValue: Box = box,
  viewport = () => ({ wide: true, narrow: false }),
  targetOverrides: Partial<AiTarget> = {},
) {
  const registry: AiRegistry = createAiRegistry({
    viewport,
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
    ...targetOverrides,
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

function addTarget(registry: AiRegistry, id: string, label: string) {
  const element = document.createElement('div')
  element.tabIndex = 0
  setBox(element, box)
  document.body.prepend(element)
  const target: AiTarget = {
    id,
    kind: 'device',
    label,
    context: { site: 'Hamburg Hafen' },
  }
  registry.register(element, target)
  return { element, target }
}

function deferred<T>() {
  let resolve: (value: T) => void = () => {}
  let reject: (reason: unknown) => void = () => {}
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve
    reject = onReject
  })
  return { promise, resolve, reject }
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

function cancelButton(): HTMLButtonElement {
  const button = [
    ...document.querySelectorAll<HTMLButtonElement>('form button'),
  ].find((candidate) => candidate.textContent?.trim() === 'Cancel')
  if (!button) throw new Error('Missing cancel button')
  return button
}

function pressTab() {
  const current = document.activeElement
  current?.dispatchEvent(
    new KeyboardEvent('keydown', {
      key: 'Tab',
      code: 'Tab',
      bubbles: true,
      cancelable: true,
    }),
  )
  const elements = [...document.querySelectorAll<HTMLElement>('*')]
  const currentIndex =
    current instanceof HTMLElement ? elements.indexOf(current) : -1
  const next = elements.slice(currentIndex + 1).find((element) => {
    if (element.tabIndex < 0) return false
    return (
      element instanceof HTMLButtonElement ||
      element instanceof HTMLInputElement ||
      element instanceof HTMLTextAreaElement ||
      element.tabIndex === 0
    )
  })
  next?.focus()
}

describe('AiActionLayer selection and Ask', () => {
  it('reveals Ask for the selected target in place', async () => {
    const { registry } = setup()
    registry.highlight('a:devices:device:d1')
    await settle()

    const button = askButton()
    expect(button).not.toBeNull()
    expect(button?.getAttribute('aria-label')).toBe('Ask about d1')
    expect(button?.textContent?.trim()).toBe('AI')
    expect(button?.style.top).toBe('124px')
    expect(button?.style.left).toBe('350px')
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

  it('does not let a late answer clear or replace a newer target request', async () => {
    const { registry } = setup()
    const second = addTarget(registry, 'a:devices:device:d2', 'd2')
    const firstRequest = deferred<string>()
    const secondRequest = deferred<string>()
    registry.onRequest((request) =>
      request.targetId === 'a:devices:device:d1'
        ? firstRequest.promise
        : secondRequest.promise,
    )
    registry.highlight('a:devices:device:d1')
    await settle()
    askButton()?.click()
    await settle()
    type('First question')
    await settle()
    submitButton().click()
    await settle()

    cancelButton().click()
    await settle()
    registry.highlight(second.target.id)
    await settle()
    askButton()?.click()
    await settle()
    type('Second question')
    await settle()
    submitButton().click()
    await settle()

    firstRequest.resolve('Late first answer')
    await settle()

    expect(document.body.textContent).not.toContain('Late first answer')
    expect(submitButton().getAttribute('aria-busy')).toBe('true')

    secondRequest.resolve('Current answer')
    await settle()
    expect(document.body.textContent).toContain('Current answer')
  })

  it('does not show a late rejection in a newer target Ask', async () => {
    const { registry } = setup()
    const second = addTarget(registry, 'a:devices:device:d2', 'd2')
    const firstRequest = deferred<string>()
    registry.onRequest(() => firstRequest.promise)
    registry.highlight('a:devices:device:d1')
    await settle()
    askButton()?.click()
    await settle()
    type('First question')
    await settle()
    submitButton().click()
    await settle()

    cancelButton().click()
    await settle()
    registry.highlight(second.target.id)
    await settle()
    askButton()?.click()
    await settle()
    type('Second question')

    firstRequest.reject(new Error('Late first failure'))
    await settle()

    expect(document.querySelector('textarea')).not.toBeNull()
    expect(document.body.textContent).toContain('Ask about d2')
    expect(document.body.textContent).not.toContain('Late first failure')
    expect(submitButton().disabled).toBe(false)
  })

  it('prefers the focused target until focus leaves it', async () => {
    const { registry } = setup()
    const second = addTarget(registry, 'a:devices:device:d2', 'd2')
    const outside = document.createElement('button')
    document.body.append(outside)
    registry.highlight('a:devices:device:d1')
    second.element.focus()
    await settle()

    expect(askButton()?.getAttribute('aria-label')).toBe('Ask about d2')

    outside.focus()
    await settle()
    expect(askButton()?.getAttribute('aria-label')).toBe('Ask about d1')
  })

  it('keeps the focused target when its Ask trigger receives a pointer click', async () => {
    const { registry } = setup()
    const second = addTarget(registry, 'a:devices:device:d2', 'd2')
    const seen: AiRequest[] = []
    registry.onRequest((request) => {
      seen.push(request)
      return 'Answer for d2'
    })
    registry.highlight('a:devices:device:d1')
    second.element.focus()
    await settle()

    const button = askButton()
    expect(button?.getAttribute('aria-label')).toBe('Ask about d2')
    button?.dispatchEvent(
      new PointerEvent('pointerdown', { bubbles: true, cancelable: true }),
    )
    button?.focus()
    button?.click()
    await settle()

    expect(document.body.textContent).toContain('Ask about d2')
    type('Question for the focused target')
    await settle()
    submitButton().click()
    await settle()

    expect(seen).toHaveLength(1)
    expect(seen[0]?.targetId).toBe(second.target.id)
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

  it('returns focus to the originating control before the next Tab', async () => {
    const { element } = setup()
    const origin = document.createElement('button')
    origin.textContent = 'Open device actions'
    element.append(origin)
    const next = document.createElement('button')
    next.textContent = 'Next control'
    element.after(next)
    origin.focus()
    await settle()

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
    expect(document.activeElement).toBeInstanceOf(HTMLTextAreaElement)

    document.activeElement?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settle()

    expect(document.activeElement).toBe(origin)
    pressTab()
    expect(document.activeElement).toBe(next)
  })

  it('returns focus to the originating control after Cancel', async () => {
    const { element } = setup()
    const origin = document.createElement('button')
    element.append(origin)
    origin.focus()
    await settle()

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
    cancelButton().click()
    await settle()

    expect(document.activeElement).toBe(origin)
  })

  it('reveals nothing on pointer entry', async () => {
    const { element } = setup()
    element.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }))
    await settle()
    expect(askButton()).toBeUndefined()
  })
})

describe('AiActionLayer geometry', () => {
  it('moves Ask above a mobile card whose corner holds a link', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(844)
    const card = { top: 494, left: 29, width: 332, height: 98 }
    const link = { top: 500, left: 270, width: 91, height: 24 }
    const { registry, element } = setup(card)
    const page = document.createElement('div')
    page.tabIndex = 0
    setBox(page, { top: 114, left: 0, width: 390, height: 730 })
    element.replaceWith(page)
    page.append(element)
    const linkElement = document.createElement('a')
    linkElement.href = '#device'
    setBox(linkElement, link)
    element.append(linkElement)
    registry.highlight('a:devices:device:d1')
    await settle()

    const button = askButton()
    expect(button?.style.top).toBe('468px')
    expect(button?.style.left).toBe('331px')
    expect(boxesIntersect(buttonBox(button), link)).toBe(false)
    button?.dispatchEvent(
      new PointerEvent('pointerdown', { bubbles: true, cancelable: true }),
    )
    button?.click()
    await settle()
    expect(document.querySelector('textarea')).not.toBeNull()
  })

  it('keeps Ask clear of controls in adjacent mobile rows', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(844)
    const card = { top: 593, left: 29, width: 332, height: 98 }
    const previousRow = { top: 494, left: 29, width: 332, height: 98 }
    const nextRow = { top: 692, left: 29, width: 332, height: 98 }
    const { registry, element } = setup(card)
    const previousControl = document.createElement('button')
    const nextControl = document.createElement('button')
    setBox(previousControl, previousRow)
    setBox(nextControl, nextRow)
    element.before(previousControl)
    element.after(nextControl)
    registry.highlight('a:devices:device:d1')
    await settle()

    const ask = buttonBox(askButton())
    expect(ask).toEqual({ top: 597, left: 331, width: 26, height: 22 })
    expect(boxesIntersect(ask, previousRow)).toBe(false)
    expect(boxesIntersect(ask, nextRow)).toBe(false)
  })

  it('keeps Ask on screen for a small target clipped by the viewport edge', async () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(390)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(844)
    const clippedTarget = { top: -10, left: 380, width: 30, height: 20 }
    const { registry } = setup(clippedTarget)
    registry.highlight('a:devices:device:d1')
    await settle()

    const button = askButton()
    expect(button?.style.top).toBe('4px')
    expect(
      Number.parseFloat(button?.style.left ?? '') + 26,
    ).toBeLessThanOrEqual(390)
  })

  it('stacks the action layer below the app chrome and modal layers', async () => {
    const { registry } = setup()
    const styles = document.createElement('style')
    const topbarRule = appStyles.match(/\.topbar\s*\{[^}]*\}/)?.[0]
    expect(topbarRule).toBeDefined()
    styles.dataset.testAppStyles = ''
    styles.textContent = topbarRule ?? ''
    document.head.append(styles)
    const topbar = document.createElement('header')
    topbar.className = 'topbar z-50'
    document.body.append(topbar)
    registry.highlight('a:devices:device:d1')
    await settle()

    const layer = document.querySelector<HTMLElement>('.ai-layer')
    if (!layer) throw new Error('Missing action layer')
    const layerZ = Number(getComputedStyle(layer).zIndex)
    const topbarZ = Number(getComputedStyle(topbar).zIndex)
    expect(layerZ).toBeGreaterThan(0)
    expect(topbarZ).toBe(2)
    expect(layerZ).toBeLessThan(topbarZ)
    expect(layerZ).toBeLessThan(50)
  })

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

  it('drops a selected responsive copy when the viewport changes', async () => {
    let wide = true
    const { registry, target } = setup(box, () => ({ wide, narrow: !wide }), {
      segment: 'desktop',
    })
    registry.highlight(target.id)
    await settle()
    expect(askButton()).not.toBeNull()

    wide = false
    window.dispatchEvent(new Event('resize'))
    await settle()

    expect(registry.selection()).toBeUndefined()
    expect(askButton()).toBeUndefined()
  })
})

describe('AiActionLayer placement invariant', () => {
  const viewport = { width: 390, height: 844 }

  async function render(target: Box, controls: Box[]) {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(viewport.width)
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(viewport.height)
    const { registry, element } = setup(target)
    for (const control of controls) {
      const node = document.createElement('button')
      setBox(node, control)
      document.body.append(node)
    }
    registry.highlight('a:devices:device:d1')
    await settle()
    return { element }
  }

  function expectClear(
    button: HTMLButtonElement | undefined,
    controls: Box[],
  ): void {
    expect(button).toBeDefined()
    const ask = buttonBox(button)
    expect(ask.left).toBeGreaterThanOrEqual(0)
    expect(ask.top).toBeGreaterThanOrEqual(0)
    expect(ask.left + ask.width).toBeLessThanOrEqual(viewport.width)
    expect(ask.top + ask.height).toBeLessThanOrEqual(viewport.height)
    for (const control of controls)
      expect(boxesIntersect(ask, control)).toBe(false)
  }

  const card: Box = { top: 593, left: 29, width: 332, height: 98 }
  const previousRow: Box = { top: 494, left: 29, width: 332, height: 98 }
  const nextRow: Box = { top: 692, left: 29, width: 332, height: 98 }

  it('keeps the 390px adjacent-card Ask on screen and off controls', async () => {
    await render(card, [previousRow, nextRow])

    const button = askButton()
    expectClear(button, [previousRow, nextRow])
    expect(buttonBox(button)).toEqual({
      top: 597,
      left: 331,
      width: 26,
      height: 22,
    })
  })

  it('moves above the corner when a control occupies it', async () => {
    const corner: Box = { top: 595, left: 331, width: 40, height: 30 }
    await render(card, [nextRow, corner])

    const button = askButton()
    expectClear(button, [nextRow, corner])
    expect(buttonBox(button).top).toBe(567)
  })

  it('keeps a wider target clear of a control just below it', async () => {
    const target: Box = { top: 120, left: 60, width: 320, height: 40 }
    const below: Box = { top: 170, left: 60, width: 320, height: 40 }
    await render(target, [below])

    expectClear(askButton(), [below])
  })

  const rightColumn: Box = { top: 560, left: 330, width: 60, height: 210 }
  const leftColumn: Box = { top: 600, left: 0, width: 29, height: 80 }

  it('hides Ask when no candidate is both on screen and clear', async () => {
    await render(card, [rightColumn, leftColumn])

    const button = askButton()
    expect(button).toBeDefined()
    expect(button?.style.display).toBe('none')
    expect(button?.style.top).toBe('')
    expect(button?.style.left).toBe('')
  })

  it('keeps an open panel mounted while the trigger stays hidden', async () => {
    const { element } = await render(card, [rightColumn, leftColumn])
    element.focus()
    await settle()

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

    expect(document.querySelector('form textarea')).not.toBeNull()
    expect(askButton()?.style.display).toBe('none')
  })
})
