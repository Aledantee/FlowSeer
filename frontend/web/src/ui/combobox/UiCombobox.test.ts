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

async function settleCombobox() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
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

    await settleCombobox()

    const input = host.querySelector('input')
    expect(input).not.toBeNull()

    // Type "ban"
    if (input) {
      input.value = 'ban'
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    await settleCombobox()

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

    await settleCombobox()

    const input = host.querySelector('input')
    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowDown',
        code: 'ArrowDown',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settleCombobox()

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

    await settleCombobox()

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
    await settleCombobox()

    // Press Enter
    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settleCombobox()

    expect(selected.value).toBe('banana')
  })

  it('round-trips an empty All option through the public model', async () => {
    const selected = ref('apple')
    mountApp(() =>
      h(UiCombobox, {
        options: [{ value: '', label: 'All' }, ...sampleOptions],
        defaultOpen: true,
        modelValue: selected.value,
        'onUpdate:modelValue': (value: string | string[]) => {
          if (typeof value === 'string') selected.value = value
        },
      }),
    )

    await settleCombobox()

    const allOption = Array.from(
      document.body.querySelectorAll<HTMLElement>('[role="option"]'),
    ).find((option) => option.textContent?.includes('All'))
    allOption?.click()
    await settleCombobox()

    expect(selected.value).toBe('')
  })

  it('selects and deselects array values and renders slot membership', async () => {
    const selected = ref<string[]>(['apple'])
    mountApp(() =>
      h(
        UiCombobox,
        {
          options: sampleOptions,
          defaultOpen: true,
          modelValue: selected.value,
          'onUpdate:modelValue': (value: string | string[]) => {
            if (Array.isArray(value)) selected.value = value
          },
        },
        {
          item: ({
            option,
            selected: isSelected,
          }: {
            option: ComboboxOption
            selected: boolean
          }) =>
            h(
              'span',
              {
                'data-value': option.value,
                'data-selected': String(isSelected),
              },
              option.label,
            ),
        },
      ),
    )

    await settleCombobox()

    const option = (value: string) =>
      document.body
        .querySelector<HTMLElement>(`[data-value="${value}"]`)
        ?.closest<HTMLElement>('[role="option"]')

    expect(option('apple')?.getAttribute('aria-selected')).toBe('true')
    expect(
      document.body.querySelector<HTMLElement>('[data-value="apple"]')?.dataset
        .selected,
    ).toBe('true')
    expect(
      document.body.querySelector<HTMLElement>('[data-value="banana"]')?.dataset
        .selected,
    ).toBe('false')

    option('banana')?.click()
    await settleCombobox()

    expect(selected.value).toEqual(['apple', 'banana'])
    expect(option('banana')?.getAttribute('aria-selected')).toBe('true')

    option('apple')?.click()
    await settleCombobox()

    expect(selected.value).toEqual(['banana'])
    expect(option('apple')?.getAttribute('aria-selected')).toBe('false')
  })

  it('emits select once for a pointer pick', async () => {
    const selectedValues: string[] = []
    mountApp(() =>
      h(UiCombobox, {
        options: sampleOptions,
        defaultOpen: true,
        onSelect: (value: string) => selectedValues.push(value),
      }),
    )

    await settleCombobox()

    document.body.querySelector<HTMLElement>('[role="option"]')?.click()
    await settleCombobox()

    expect(selectedValues).toEqual(['apple'])
  })

  it('emits select once for a keyboard pick', async () => {
    const selectedValues: string[] = []
    const host = mountApp(() =>
      h(UiCombobox, {
        options: sampleOptions,
        defaultOpen: true,
        onSelect: (value: string) => selectedValues.push(value),
      }),
    )

    await settleCombobox()

    const input = host.querySelector('input')
    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowDown',
        code: 'ArrowDown',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settleCombobox()
    input?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settleCombobox()

    expect(selectedValues).toEqual(['banana'])
  })
})
