// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiCombobox, { type ComboboxOption } from './UiCombobox.vue'

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

const sampleOptions: ComboboxOption[] = [
  { value: 'apple', label: 'Apple' },
  { value: 'banana', label: 'Banana' },
  { value: 'cherry', label: 'Cherry' },
]

describe('UiCombobox', () => {
  it('typing in input filters options when ignoreFilter is false', async () => {
    const host = mountApp(() =>
      h(UiCombobox, {
        options: sampleOptions,
        defaultOpen: true,
        ignoreFilter: false,
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = host.querySelector('input')
    expect(input).not.toBeNull()

    // Type "ban"
    if (input) {
      input.value = 'ban'
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const items = document.body.querySelectorAll('[role="option"]')
    expect(items.length).toBe(1)
    expect(items[0]?.textContent).toContain('Banana')
  })

  it('arrow keys highlight items with data-highlighted and update aria-activedescendant', async () => {
    const host = mountApp(() =>
      h(UiCombobox, {
        options: sampleOptions,
        defaultOpen: true,
      }),
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

    const items = document.body.querySelectorAll('[role="option"]')
    expect(items.length).toBeGreaterThan(0)
    const highlighted = Array.from(items).find((item) =>
      item.hasAttribute('data-highlighted'),
    )
    expect(highlighted).toBeDefined()
    expect(input?.getAttribute('aria-activedescendant')).toBeTruthy()
  })

  it('enter selects highlighted item and emits update', async () => {
    const selected = ref('')
    const host = mountApp(() =>
      h(UiCombobox, {
        options: sampleOptions,
        defaultOpen: true,
        modelValue: selected.value,
        'onUpdate:modelValue': (v: string | string[]) => {
          selected.value = v as string
        },
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const input = host.querySelector('input')
    // Highlight next item (banana)
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

    expect(selected.value).toBe('banana')
  })
})
