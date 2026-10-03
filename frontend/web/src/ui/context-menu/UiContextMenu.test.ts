// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiContextMenu, { type UiContextMenuProps } from './UiContextMenu.vue'
import UiContextMenuItem from './UiContextMenuItem.vue'
import UiContextMenuSeparator from './UiContextMenuSeparator.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountMenu(
  props: Partial<UiContextMenuProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiContextMenu, props as UiContextMenuProps, slots)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiContextMenu', () => {
  it('opens on cancelable contextmenu and is default-prevented after an awaited tick', async () => {
    const host = mountMenu(
      {},
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiContextMenuItem, { label: 'Item 1' }),
          h(UiContextMenuItem, { label: 'Item 2' }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    expect(trigger).not.toBeNull()
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    const event = new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      clientX: 50,
      clientY: 50,
    })
    trigger?.dispatchEvent(event)

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(event.defaultPrevented).toBe(true)
    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()
    expect(document.body.textContent).toContain('Item 1')
    expect(document.body.textContent).toContain('Item 2')
  })

  it('renders label and hint with UiKbd', async () => {
    const host = mountMenu(
      {},
      {
        trigger: () => h('div', { id: 'menu-trigger' }, 'Area'),
        default: () => [
          h(UiContextMenuItem, { label: 'Inspect', hint: '⌥⌘I' }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLElement>('#menu-trigger')
    trigger?.dispatchEvent(
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
    expect(menu?.textContent).toContain('Inspect')
    const kbd = menu?.querySelector('kbd')
    expect(kbd).not.toBeNull()
    expect(kbd?.textContent).toContain('⌥⌘I')
  })

  it('arrow keys and Enter select an item', async () => {
    let selected = false
    const host = mountMenu(
      {},
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiContextMenuItem, {
            id: 'item-1',
            label: 'Item 1',
            onSelect: () => {
              selected = true
            },
          }),
          h(UiContextMenuItem, { id: 'item-2', label: 'Item 2' }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    trigger?.dispatchEvent(
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

    menu?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowDown',
        code: 'ArrowDown',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const activeItem =
      (document.activeElement as HTMLElement) ??
      document.body.querySelector('#item-1')
    activeItem?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(selected).toBe(true)
  })

  it('disabled item rejects selection', async () => {
    let disabledSelected = false
    const host = mountMenu(
      {},
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiContextMenuItem, {
            id: 'disabled-item',
            label: 'Disabled',
            disabled: true,
            onSelect: () => {
              disabledSelected = true
            },
          }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    trigger?.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const item = document.body.querySelector('#disabled-item') as HTMLElement
    expect(item).not.toBeNull()
    expect(item.hasAttribute('data-disabled')).toBe(true)

    item.click()
    await nextTick()
    expect(disabledSelected).toBe(false)
  })

  it('renders separator with role="separator"', async () => {
    const host = mountMenu(
      {},
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiContextMenuItem, { label: 'Item 1' }),
          h(UiContextMenuSeparator),
          h(UiContextMenuItem, { label: 'Item 2' }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    trigger?.dispatchEvent(
      new MouseEvent('contextmenu', {
        bubbles: true,
        cancelable: true,
        clientX: 50,
        clientY: 50,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const separator = document.body.querySelector('[role="separator"]')
    expect(separator).not.toBeNull()
  })

  it('Escape closes the menu and returns focus', async () => {
    const host = mountMenu(
      {},
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiContextMenuItem, { id: 'item-1', label: 'Item 1' }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    trigger?.focus()
    expect(document.activeElement).toBe(trigger)

    trigger?.dispatchEvent(
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

    menu?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('emits closeAutoFocus on close and honors preventDefault to preserve custom focus', async () => {
    let emittedEvent: Event | null = null
    const customButton = document.createElement('button')
    customButton.id = 'menu-custom-focus'
    document.body.append(customButton)

    const host = mountMenu(
      {
        onCloseAutoFocus: (e: Event) => {
          emittedEvent = e
          e.preventDefault()
          customButton.focus()
        },
      },
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiContextMenuItem, { id: 'item-1', label: 'Item 1' }),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    trigger?.dispatchEvent(
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

    menu?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(emittedEvent).not.toBeNull()
    expect(document.activeElement).toBe(customButton)
    customButton.remove()
  })
})
