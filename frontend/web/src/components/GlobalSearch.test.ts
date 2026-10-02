// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { TooltipProvider } from 'reka-ui'
import type { SearchResult } from '../domain/search'
import { SHORTCUTS, isMac, keysOf } from '../navigation/shortcuts'
import GlobalSearch, { type SearchPage } from './GlobalSearch.vue'
import { createWebI18n } from '../i18n'

const pages: SearchPage[] = [
  {
    id: 'overview',
    title: 'Page One',
    detail: 'First page',
    icon: 'dashboard',
  },
  {
    id: 'inventory',
    title: 'Page Two',
    detail: 'Second page',
    icon: 'devices',
  },
]

let dispose = () => {}

afterEach(() => {
  dispose()
  localStorage.clear()
  document.body.replaceChildren()
})

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
}

interface SearchHandlers {
  onSelect?: (result: SearchResult, beside: boolean) => void
  onDock?: (result: SearchResult) => void
}

async function mountSearchClosed(handlers: SearchHandlers = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () =>
      h(TooltipProvider, null, () =>
        h(GlobalSearch, {
          fleet: [],
          pages,
          canSplit: true,
          ...handlers,
        }),
      ),
  })
  app.use(createWebI18n())
  app.mount(host)
  dispose = () => app.unmount()

  await settle()
  return { host }
}

async function mountSearch(handlers: SearchHandlers) {
  const { host } = await mountSearchClosed(handlers)

  host.querySelector<HTMLButtonElement>('.search-trigger')?.click()
  await settle()

  const input =
    document.body.querySelector<HTMLInputElement>('[role="combobox"]')
  expect(input).not.toBeNull()
  if (!input) throw new Error('Global search input did not open.')

  input.value = 'Page'
  input.dispatchEvent(new InputEvent('input', { bubbles: true }))
  await settle()

  const options = document.body.querySelectorAll<HTMLElement>('[role="option"]')
  expect(options).toHaveLength(2)
  return { input, options }
}

function keydown(input: HTMLInputElement, init: KeyboardEventInit) {
  input.dispatchEvent(
    new KeyboardEvent('keydown', {
      key: 'Enter',
      code: 'Enter',
      bubbles: true,
      cancelable: true,
      ...init,
    }),
  )
}

describe('global search selection', () => {
  it('opens the highlighted result once with ordinary Enter', async () => {
    const selected: [SearchResult, boolean][] = []
    const { input } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    keydown(input, {})
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'overview' }), false],
    ])
  })

  it('opens a clicked result normally', async () => {
    const selected: [SearchResult, boolean][] = []
    const { options } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    options[1]?.click()
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'inventory' }), false],
    ])
  })

  it('opens the non-first highlighted result once with Shift+Enter', async () => {
    const selected: [SearchResult, boolean][] = []
    const { input } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    keydown(input, { key: 'ArrowDown', code: 'ArrowDown' })
    await settle()
    keydown(input, { shiftKey: true })
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'inventory' }), true],
    ])
  })

  it('sends the highlighted result to the dock once with Alt+Enter', async () => {
    const docked: SearchResult[] = []
    const { input } = await mountSearch({
      onDock: (result) => docked.push(result),
    })

    keydown(input, { altKey: true })
    await settle()

    expect(docked).toEqual([expect.objectContaining({ id: 'overview' })])
  })

  it('preserves modifier-click side-by-side behavior', async () => {
    const selected: [SearchResult, boolean][] = []
    const { options } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    options[1]?.dispatchEvent(
      new MouseEvent('click', {
        bubbles: true,
        cancelable: true,
        metaKey: true,
      }),
    )
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'inventory' }), true],
    ])
  })
})

describe('global search shortcuts', () => {
  it('displays registered shortcut keys and opens search with the registered shortcut', async () => {
    const { host } = await mountSearchClosed()
    const trigger = host.querySelector<HTMLButtonElement>('.search-trigger')
    const kbd = trigger?.querySelector('kbd')
    expect(kbd?.textContent?.trim()).toBe(
      keysOf(SHORTCUTS.search).join(isMac() ? '' : ' '),
    )

    const event = new KeyboardEvent('keydown', {
      key: 'k',
      code: SHORTCUTS.search.code,
      bubbles: true,
      cancelable: true,
      ...(isMac() ? { metaKey: true } : { ctrlKey: true }),
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(true)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).not.toBeNull()
  })

  it('does not open search when the platform modifier does not match', async () => {
    await mountSearchClosed()

    const event = new KeyboardEvent('keydown', {
      key: 'k',
      code: SHORTCUTS.search.code,
      bubbles: true,
      cancelable: true,
      ...(isMac() ? { ctrlKey: true } : { metaKey: true }),
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })

  it('does not open search with Cmd/Ctrl+K when a modal is already open', async () => {
    await mountSearchClosed()

    const modal = document.createElement('div')
    modal.setAttribute('role', 'dialog')
    modal.dataset.state = 'open'
    document.body.append(modal)

    const event = new KeyboardEvent('keydown', {
      key: 'k',
      code: 'KeyK',
      bubbles: true,
      cancelable: true,
      ...(isMac() ? { metaKey: true } : { ctrlKey: true }),
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })

  it('does not open search with / when focused in a contenteditable element', async () => {
    await mountSearchClosed()

    const editable = document.createElement('div')
    editable.contentEditable = 'true'
    document.body.append(editable)
    editable.focus()

    const event = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      bubbles: true,
      cancelable: true,
    })
    editable.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })

  it('opens search with / when not typing, but not when a modal is open', async () => {
    await mountSearchClosed()

    const modal = document.createElement('div')
    modal.setAttribute('role', 'dialog')
    modal.dataset.state = 'open'
    document.body.append(modal)

    const blockedEvent = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(blockedEvent)
    await settle()

    expect(blockedEvent.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()

    modal.remove()

    const allowedEvent = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(allowedEvent)
    await settle()

    expect(allowedEvent.defaultPrevented).toBe(true)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).not.toBeNull()
  })

  it('does not open search with / when modified by Shift', async () => {
    await mountSearchClosed()

    const event = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })
})
