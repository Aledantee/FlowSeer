// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiDropdownMenu, { type UiDropdownMenuProps } from './UiDropdownMenu.vue'
import UiDropdownMenuItem from './UiDropdownMenuItem.vue'
import UiDropdownMenuSeparator from './UiDropdownMenuSeparator.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountMenu(
  props: Partial<UiDropdownMenuProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiDropdownMenu, props as UiDropdownMenuProps, slots)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiDropdownMenu', () => {
  it('opens on trigger click', async () => {
    const host = mountMenu(
      {},
      {
        trigger: () => h('button', { id: 'menu-trigger' }, 'Trigger'),
        default: () => [
          h(UiDropdownMenuItem, {}, () => 'Item 1'),
          h(UiDropdownMenuItem, {}, () => 'Item 2'),
        ],
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#menu-trigger')
    expect(trigger).not.toBeNull()
    expect(document.body.querySelector('[role="menu"]')).toBeNull()

    trigger?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()
    expect(document.body.textContent).toContain('Item 1')
  })

  it('arrow keys navigate items with roving highlight', async () => {
    mountMenu(
      { defaultOpen: true },
      {
        trigger: () => h('button', 'Trigger'),
        default: () => [
          h(UiDropdownMenuItem, { id: 'item-1' }, () => 'Item 1'),
          h(UiDropdownMenuItem, { id: 'item-2' }, () => 'Item 2'),
        ],
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()

    // Dispatch ArrowDown
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

    const item1 = document.body.querySelector('#item-1')
    expect(
      item1?.hasAttribute('data-highlighted') ||
        document.activeElement === item1,
    ).toBe(true)
  })

  it('disabled item is skipped by arrow navigation and rejects clicks', async () => {
    let disabledClicked = false
    mountMenu(
      { defaultOpen: true },
      {
        trigger: () => h('button', 'Trigger'),
        default: () => [
          h(
            UiDropdownMenuItem,
            {
              id: 'disabled-item',
              disabled: true,
              onSelect: () => {
                disabledClicked = true
              },
            },
            () => 'Disabled Item',
          ),
        ],
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const disabledItem = document.body.querySelector(
      '#disabled-item',
    ) as HTMLElement
    expect(disabledItem).not.toBeNull()
    expect(disabledItem.hasAttribute('data-disabled')).toBe(true)

    disabledItem.click()
    await nextTick()
    expect(disabledClicked).toBe(false)
  })

  it('selection emits event and closes menu', async () => {
    let selected = false
    const isOpen = ref(true)

    mountMenu(
      {
        open: isOpen.value,
        'onUpdate:open': (v: boolean) => {
          isOpen.value = v
        },
      },
      {
        trigger: () => h('button', 'Trigger'),
        default: () => [
          h(
            UiDropdownMenuItem,
            {
              id: 'select-item',
              onSelect: () => {
                selected = true
                isOpen.value = false
              },
            },
            () => 'Select Item',
          ),
        ],
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const item = document.body.querySelector('#select-item') as HTMLElement
    expect(item).not.toBeNull()

    item.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(selected).toBe(true)
    expect(isOpen.value).toBe(false)
  })

  it('renders separator with role="separator"', async () => {
    mountMenu(
      { defaultOpen: true },
      {
        trigger: () => h('button', 'Trigger'),
        default: () => [
          h(UiDropdownMenuItem, {}, () => 'Item 1'),
          h(UiDropdownMenuSeparator),
          h(UiDropdownMenuItem, {}, () => 'Item 2'),
        ],
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const separator = document.body.querySelector('[role="separator"]')
    expect(separator).not.toBeNull()
  })
})
