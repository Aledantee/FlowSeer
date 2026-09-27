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

    expect(selected.value).toBe('banana')
  })

  it('handles empty-string valued option without throwing, opens and selects it', async () => {
    const selected = ref('site-1')
    const selectEmissions: string[] = []
    const emptyOptions: ComboboxOption[] = [
      { value: '', label: 'All sites' },
      { value: 'site-1', label: 'Site 1' },
      { value: 'site-2', label: 'Site 2' },
    ]

    mountApp(() =>
      h(UiCombobox, {
        options: emptyOptions,
        defaultOpen: true,
        modelValue: selected.value,
        'onUpdate:modelValue': (v: string | string[]) => {
          selected.value = v as string
        },
        onSelect: (v: string) => {
          selectEmissions.push(v)
        },
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const options = document.body.querySelectorAll('[role="option"]')
    expect(options.length).toBe(3)
    expect(options[0]?.textContent).toContain('All sites')

    options[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(selected.value).toBe('')
    expect(selectEmissions).toEqual([''])
  })

  it('emits select exactly once on mouse click for ungrouped options', async () => {
    const selectEmissions: string[] = []
    mountApp(() =>
      h(UiCombobox, {
        options: sampleOptions,
        defaultOpen: true,
        onSelect: (v: string) => {
          selectEmissions.push(v)
        },
      }),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const options = document.body.querySelectorAll('[role="option"]')
    options[1]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(selectEmissions).toHaveLength(1)
    expect(selectEmissions[0]).toBe('banana')
  })

  it('emits select exactly once on keyboard enter for grouped options', async () => {
    const selectEmissions: string[] = []
    const groupedList: ComboboxOption[] = [
      { value: 'gw-0', label: 'Gateway 0', group: 'Gateways' },
      { value: '', label: 'All scopes', group: 'General' },
    ]

    const host = mountApp(() =>
      h(UiCombobox, {
        options: groupedList,
        defaultOpen: true,
        onSelect: (v: string) => {
          selectEmissions.push(v)
        },
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

    expect(selectEmissions).toHaveLength(1)
    expect(selectEmissions[0]).toBe('')
  })

  it('selects and deselects array values including the empty option', async () => {
    const selected = ref<string[]>([''])
    const options: ComboboxOption[] = [
      { value: '', label: 'All' },
      { value: 'alpha', label: 'Alpha' },
      { value: 'beta', label: 'Beta' },
    ]

    mountApp(() =>
      h(
        UiCombobox,
        {
          options,
          defaultOpen: true,
          modelValue: selected.value,
          'onUpdate:modelValue': (value: string | string[]) => {
            if (Array.isArray(value)) selected.value = value
          },
        },
        {
          item: ({ option }: { option: ComboboxOption }) =>
            h('span', { 'data-value': option.value }, option.label),
        },
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const option = (value: string) =>
      document.body
        .querySelector<HTMLElement>(`[data-value="${value}"]`)
        ?.closest<HTMLElement>('[role="option"]')

    expect(option('')?.getAttribute('aria-selected')).toBe('true')

    option('alpha')?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(selected.value).toEqual(['', 'alpha'])

    option('')?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(selected.value).toEqual(['alpha'])
  })

  it('forwards public highlight event and supplies scoped active state to item slot', async () => {
    const highlights: Array<{ ref?: HTMLElement; value?: string } | undefined> =
      []
    const host = mountApp(() =>
      h(
        UiCombobox,
        {
          options: sampleOptions,
          defaultOpen: true,
          onHighlight: (item: unknown) => {
            highlights.push(
              item as { ref?: HTMLElement; value?: string } | undefined,
            )
          },
        },
        {
          item: ({
            option,
            selected,
            active,
          }: {
            option: ComboboxOption
            selected: boolean
            active: boolean
          }) =>
            h(
              'span',
              {
                'data-value': option.value,
                'data-active': String(active),
                'data-selected': String(selected),
              },
              `${option.label} (active: ${active})`,
            ),
        },
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(highlights.length).toBeGreaterThan(0)
    expect(highlights[highlights.length - 1]?.value).toBe('apple')

    let activeItems = document.body.querySelectorAll('[data-active="true"]')
    expect(activeItems.length).toBe(1)
    expect(activeItems[0]?.getAttribute('data-value')).toBe('apple')

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

    expect(highlights[highlights.length - 1]?.value).toBe('banana')
    activeItems = document.body.querySelectorAll('[data-active="true"]')
    expect(activeItems.length).toBe(1)
    expect(activeItems[0]?.getAttribute('data-value')).toBe('banana')
  })

  it('supplies scoped active state for grouped options including empty value', async () => {
    const highlights: Array<{ ref?: HTMLElement; value?: string } | undefined> =
      []
    const groupedList: ComboboxOption[] = [
      { value: '', label: 'All', group: 'Scope' },
      { value: 'node-1', label: 'Node 1', group: 'Nodes' },
    ]

    const host = mountApp(() =>
      h(
        UiCombobox,
        {
          options: groupedList,
          defaultOpen: true,
          onHighlight: (item: unknown) => {
            highlights.push(
              item as { ref?: HTMLElement; value?: string } | undefined,
            )
          },
        },
        {
          item: ({
            option,
            active,
          }: {
            option: ComboboxOption
            active: boolean
          }) =>
            h(
              'span',
              {
                'data-group-value': option.value,
                'data-group-active': String(active),
              },
              `${option.label} (active: ${active})`,
            ),
        },
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(highlights.length).toBeGreaterThan(0)
    expect(highlights[highlights.length - 1]?.value).toBe('')

    let activeSpan = document.body.querySelector('[data-group-active="true"]')
    expect(activeSpan?.getAttribute('data-group-value')).toBe('')

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

    expect(highlights[highlights.length - 1]?.value).toBe('node-1')
    activeSpan = document.body.querySelector('[data-group-active="true"]')
    expect(activeSpan?.getAttribute('data-group-value')).toBe('node-1')
  })

  it('renders custom trigger as keyboard-focusable with stable aria-controls and opens on Enter and Space', async () => {
    const host = mountApp(() =>
      h(
        UiCombobox,
        {
          options: sampleOptions,
        },
        {
          trigger: () =>
            h('button', { class: 'custom-trigger' }, 'Select an option'),
        },
      ),
    )

    await nextTick()
    const trigger = host.querySelector('button.custom-trigger')
    expect(trigger).not.toBeNull()
    expect(trigger?.getAttribute('tabindex')).toBe('0')

    const ariaControls = trigger?.getAttribute('aria-controls')
    expect(ariaControls).toBeTruthy()
    expect(ariaControls).not.toBe('')
    expect(document.getElementById(ariaControls!)).toBeNull()

    trigger?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const listbox = document.getElementById(ariaControls!)
    expect(listbox).not.toBeNull()
    const options = document.body.querySelectorAll('[role="option"]')
    expect(options.length).toBe(3)

    trigger?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        code: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(document.getElementById(ariaControls!)).toBeNull()

    trigger?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: ' ',
        code: 'Space',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(document.getElementById(ariaControls!)).not.toBeNull()
  })

  it('shows public labels in a custom-trigger input and keeps filtering usable', async () => {
    const selected = ref('')
    const host = mountApp(() =>
      h(
        UiCombobox,
        {
          options: [
            { value: '', label: 'All sites' },
            { value: 'berlin', label: 'Berlin Mitte' },
            { value: 'hamburg', label: 'Hamburg Hafen' },
          ],
          modelValue: selected.value,
          placeholder: 'Search sites',
          'onUpdate:modelValue': (value: string | string[]) => {
            if (typeof value === 'string') selected.value = value
          },
        },
        {
          trigger: () => h('button', { class: 'scope-trigger' }, 'Sites'),
        },
      ),
    )

    const trigger = host.querySelector<HTMLButtonElement>('.scope-trigger')
    trigger?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    let input = document.body.querySelector<HTMLInputElement>(
      'input[placeholder="Search sites"]',
    )
    expect(input?.value).toBe('All sites')
    expect(input?.value).not.toContain('__ui_combobox_empty__')

    const berlin = Array.from(
      document.body.querySelectorAll<HTMLElement>('[role="option"]'),
    ).find((option) => option.textContent?.includes('Berlin Mitte'))
    berlin?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(selected.value).toBe('berlin')

    trigger?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    input = document.body.querySelector<HTMLInputElement>(
      'input[placeholder="Search sites"]',
    )
    expect(input?.value).toBe('Berlin Mitte')

    if (input) {
      input.value = 'ham'
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const visibleOptions = document.body.querySelectorAll('[role="option"]')
    expect(visibleOptions).toHaveLength(1)
    expect(visibleOptions[0]?.textContent).toContain('Hamburg Hafen')
  })
})
