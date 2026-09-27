// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { TooltipProvider } from 'reka-ui'
import type { SearchResult } from '../domain/search'
import GlobalSearch, { type SearchPage } from './GlobalSearch.vue'

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

async function mountSearch(handlers: {
  onSelect?: (result: SearchResult, beside: boolean) => void
  onDock?: (result: SearchResult) => void
}) {
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
  app.mount(host)
  dispose = () => app.unmount()

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
