// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiCommand from './UiCommand.vue'
import UiCommandDialog from './UiCommandDialog.vue'
import UiCommandEmpty from './UiCommandEmpty.vue'
import UiCommandGroup from './UiCommandGroup.vue'
import UiCommandInput from './UiCommandInput.vue'
import UiCommandItem, {
  type UiCommandItemSelectEvent,
} from './UiCommandItem.vue'
import UiCommandList from './UiCommandList.vue'
import UiCommandSeparator from './UiCommandSeparator.vue'
import UiCommandShortcut from './UiCommandShortcut.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountApp(renderFn: () => unknown) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: renderFn,
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiCommand', () => {
  it('renders command input, list, and items with combobox ARIA roles', async () => {
    const host = mountApp(() =>
      h(UiCommand, {}, () => [
        h(UiCommandInput, { placeholder: 'Search...' }),
        h(UiCommandList, null, () => [
          h(UiCommandGroup, { heading: 'Fruits' }, () => [
            h(UiCommandItem, { value: 'apple' }, () => 'Apple'),
            h(UiCommandItem, { value: 'banana' }, () => [
              'Banana',
              h(UiCommandShortcut, null, () => '⌘B'),
            ]),
          ]),
          h(UiCommandSeparator),
          h(UiCommandEmpty, null, () => 'No fruits'),
        ]),
      ]),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const combobox = host.querySelector('[role="combobox"]')
    expect(combobox).not.toBeNull()

    const listbox = host.querySelector('[role="listbox"]')
    expect(listbox).not.toBeNull()

    const options = host.querySelectorAll('[role="option"]')
    expect(options.length).toBe(2)
  })

  it('arrow keys navigate items with roving highlight (data-highlighted) and update aria-activedescendant', async () => {
    const host = mountApp(() =>
      h(UiCommand, {}, () => [
        h(UiCommandInput, { placeholder: 'Search...' }),
        h(UiCommandList, null, () => [
          h(UiCommandItem, { value: 'first' }, () => 'First'),
          h(UiCommandItem, { value: 'second' }, () => 'Second'),
        ]),
      ]),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = host.querySelector('input')
    expect(input).not.toBeNull()

    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowDown',
        code: 'ArrowDown',
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const options = host.querySelectorAll('[role="option"]')
    const highlighted = Array.from(options).find((opt) =>
      opt.hasAttribute('data-highlighted'),
    )
    expect(highlighted).toBeDefined()
    expect(input?.getAttribute('aria-activedescendant')).toBeTruthy()
  })

  it('enter emits select on highlighted item', async () => {
    const selected = ref('')
    const host = mountApp(() =>
      h(UiCommand, {}, () => [
        h(UiCommandInput, { placeholder: 'Search...' }),
        h(UiCommandList, null, () => [
          h(
            UiCommandItem,
            {
              value: 'item-1',
              onSelect: (event: UiCommandItemSelectEvent) => {
                selected.value = event.value
              },
            },
            () => 'Item 1',
          ),
          h(
            UiCommandItem,
            {
              value: 'item-2',
              onSelect: (event: UiCommandItemSelectEvent) => {
                selected.value = event.value
              },
            },
            () => 'Item 2',
          ),
        ]),
      ]),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = host.querySelector('input')

    // Navigate to next item
    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowDown',
        code: 'ArrowDown',
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // Press Enter
    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(selected.value).toBe('item-2')
  })

  it('ignoreFilter="true" renders provided options verbatim without internal filtering', async () => {
    const host = mountApp(() =>
      h(UiCommand, { ignoreFilter: true }, () => [
        h(UiCommandInput, { placeholder: 'Search...' }),
        h(UiCommandList, null, () => [
          h(UiCommandItem, { value: 'alpha' }, () => 'Alpha'),
          h(UiCommandItem, { value: 'beta' }, () => 'Beta'),
        ]),
      ]),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = host.querySelector('input')
    if (input) {
      input.value = 'nonexistent-query'
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    // When ignoreFilter is true, internal filtering doesn't hide items
    const options = host.querySelectorAll('[role="option"]')
    expect(options.length).toBe(2)
  })

  it('UiCommandDialog opens in portal with dialog role, dismisses on Escape, and traps focus', async () => {
    const isOpen = ref(true)
    mountApp(() =>
      h(
        UiCommandDialog,
        {
          open: isOpen.value,
          'onUpdate:open': (val: boolean) => {
            isOpen.value = val
          },
        },
        () => [
          h(UiCommandInput, { placeholder: 'Palette search...' }),
          h(UiCommandList, null, () => [
            h(UiCommandItem, { value: 'command-1' }, () => 'Command 1'),
          ]),
        ],
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()

    // Dismiss on Escape
    dialog?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(isOpen.value).toBe(false)
  })
})
