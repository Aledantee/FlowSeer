// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, ref } from 'vue'
import UiSelect from './UiSelect.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

const options = [
  { value: 'hamburg', label: 'Hamburg Site' },
  { value: 'berlin', label: 'Berlin Site' },
]

function mountSelect(
  props: Record<string, unknown> = {},
  parentContainer?: HTMLElement,
) {
  const host = parentContainer ?? document.createElement('div')
  if (!parentContainer) {
    document.body.append(host)
  }
  const app = createApp(UiSelect, props)
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiSelect', () => {
  it('emits update:modelValue on selection', async () => {
    const selected = ref('hamburg')
    const host = mountSelect({
      modelValue: selected.value,
      options,
      'onUpdate:modelValue': (val: string) => {
        selected.value = val
      },
    })

    const trigger = host.querySelector('button')
    if (!trigger) throw new Error('Missing trigger button')

    trigger.dispatchEvent(
      new PointerEvent('pointerdown', {
        bubbles: true,
        cancelable: true,
        button: 0,
      }),
    )
    trigger.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }),
    )
    await nextTick()

    const items = document.querySelectorAll('[role="option"]')
    const berlinItem = [...items].find((item) =>
      item.textContent?.includes('Berlin'),
    ) as HTMLElement
    if (!berlinItem) throw new Error('Missing Berlin option item')
    berlinItem.focus()
    berlinItem.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(selected.value).toBe('berlin')
  })

  it('renders hidden native select or input when name is provided', () => {
    const host = mountSelect({
      modelValue: 'hamburg',
      options,
      name: 'siteId',
    })

    const hiddenControl = host.querySelector(
      'select[name="siteId"], input[name="siteId"]',
    )
    expect(hiddenControl).not.toBeNull()
  })

  it('includes selected value in form submission', async () => {
    let submittedData: Record<string, string> = {}
    const form = document.createElement('form')
    document.body.append(form)
    form.addEventListener('submit', (e: Event) => {
      e.preventDefault()
      const formData = new FormData(form)
      submittedData = Object.fromEntries(
        [...formData.entries()].map(([k, v]) => [k, String(v)]),
      )
    })

    const mountPoint = document.createElement('div')
    form.append(mountPoint)
    mountSelect(
      {
        modelValue: 'hamburg',
        options,
        name: 'siteId',
      },
      mountPoint,
    )

    const submitBtn = document.createElement('button')
    submitBtn.type = 'submit'
    form.append(submitBtn)

    submitBtn.click()
    await nextTick()

    expect(submittedData.siteId).toBe('hamburg')
  })
})
