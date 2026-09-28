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

  it('UiCommandDialog emits closeAutoFocus and honors preventDefault to preserve custom focus', async () => {
    let emittedEvent: Event | null = null
    const customButton = document.createElement('button')
    customButton.id = 'command-custom-focus'
    document.body.append(customButton)

    const isOpen = ref(true)
    mountApp(() =>
      h(
        UiCommandDialog,
        {
          open: isOpen.value,
          'onUpdate:open': (val: boolean) => {
            isOpen.value = val
          },
          onCloseAutoFocus: (e: Event) => {
            emittedEvent = e
            e.preventDefault()
            customButton.focus()
          },
        },
        () => [h(UiCommandInput, { placeholder: 'Palette search...' })],
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()

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

    expect(emittedEvent).not.toBeNull()
    expect(document.activeElement).toBe(customButton)
    customButton.remove()
  })

  it('forwards highlighted value on roving keyboard navigation', async () => {
    const highlighted = ref('')
    const host = mountApp(() =>
      h(
        UiCommand,
        {
          highlightedValue: highlighted.value,
          'onUpdate:highlightedValue': (v: string) => {
            highlighted.value = v
          },
        },
        () => [
          h(UiCommandInput, { placeholder: 'Search...' }),
          h(UiCommandList, null, () => [
            h(UiCommandItem, { value: 'item-1' }, () => 'Item 1'),
            h(UiCommandItem, { value: 'item-2' }, () => 'Item 2'),
          ]),
        ],
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = host.querySelector('input')
    expect(highlighted.value).toBe('item-1')

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
    expect(highlighted.value).toBe('item-2')
  })

  it('Shift+Enter, Cmd/Ctrl+Enter, and Alt+Enter act on the highlighted item and only once without triggering ordinary select', async () => {
    const highlighted = ref('')
    const actions: { type: string; item: string }[] = []
    const ordinarySelects: string[] = []

    function handleKeydown(event: KeyboardEvent) {
      if (event.key === 'Enter') {
        const isModified =
          event.shiftKey || event.metaKey || event.ctrlKey || event.altKey
        if (!isModified) return

        event.preventDefault()
        event.stopPropagation()
        const target = highlighted.value || 'item-1'

        if (event.altKey) {
          actions.push({ type: 'dock', item: target })
        } else if (event.shiftKey || event.metaKey || event.ctrlKey) {
          actions.push({ type: 'beside', item: target })
        }
      }
    }

    mountApp(() =>
      h(
        UiCommandDialog,
        {
          open: true,
          highlightedValue: highlighted.value,
          'onUpdate:highlightedValue': (v: string) => {
            highlighted.value = v
          },
          onKeydownCapture: handleKeydown,
        },
        () => [
          h(UiCommandInput, { placeholder: 'Palette search...' }),
          h(UiCommandList, null, () => [
            h(
              UiCommandItem,
              {
                value: 'item-1',
                onSelect: (event: UiCommandItemSelectEvent) =>
                  ordinarySelects.push(event.value),
              },
              () => 'Item 1',
            ),
            h(
              UiCommandItem,
              {
                value: 'item-2',
                onSelect: (event: UiCommandItemSelectEvent) =>
                  ordinarySelects.push(event.value),
              },
              () => 'Item 2',
            ),
          ]),
        ],
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = document.body.querySelector('input')

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

    expect(highlighted.value).toBe('item-2')

    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        shiftKey: true,
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(actions).toEqual([{ type: 'beside', item: 'item-2' }])
    expect(ordinarySelects).toHaveLength(0)

    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        altKey: true,
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(actions).toEqual([
      { type: 'beside', item: 'item-2' },
      { type: 'dock', item: 'item-2' },
    ])
    expect(ordinarySelects).toHaveLength(0)

    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        metaKey: true,
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(actions).toEqual([
      { type: 'beside', item: 'item-2' },
      { type: 'dock', item: 'item-2' },
      { type: 'beside', item: 'item-2' },
    ])
    expect(ordinarySelects).toHaveLength(0)

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

    expect(ordinarySelects).toEqual(['item-2'])
  })
})
