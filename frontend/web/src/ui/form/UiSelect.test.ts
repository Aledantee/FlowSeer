// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, ref } from 'vue'
import UiSelect from './UiSelect.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

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
  locale: WebLocale = 'en',
) {
  const host = parentContainer ?? document.createElement('div')
  if (!parentContainer) {
    document.body.append(host)
  }
  const app = createApp(UiSelect, props)
  app.use(createWebI18n(locale))
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  return host
}

async function selectOption(host: HTMLElement, optionText: string) {
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
  const targetItem = [...items].find((item) =>
    item.textContent?.includes(optionText),
  ) as HTMLElement
  if (!targetItem) throw new Error(`Missing option item for ${optionText}`)
  targetItem.focus()
  targetItem.dispatchEvent(
    new KeyboardEvent('keydown', {
      key: 'Enter',
      bubbles: true,
      cancelable: true,
    }),
  )
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 50))
  await nextTick()
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

    await selectOption(host, 'Berlin')
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

  it('submits selected value in uncontrolled use inside form', async () => {
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
        options,
        name: 'siteId',
      },
      mountPoint,
    )

    await selectOption(mountPoint, 'Berlin')

    const submitBtn = document.createElement('button')
    submitBtn.type = 'submit'
    form.append(submitBtn)

    submitBtn.click()
    await nextTick()

    expect(submittedData.siteId).toBe('berlin')
  })

  it('validates native required constraint inside form', async () => {
    const form = document.createElement('form')
    document.body.append(form)

    const mountPoint = document.createElement('div')
    form.append(mountPoint)
    mountSelect(
      {
        options,
        name: 'siteId',
        required: true,
      },
      mountPoint,
    )

    expect(form.checkValidity()).toBe(false)

    await selectOption(mountPoint, 'Hamburg')
    expect(form.checkValidity()).toBe(true)
  })

  it('mounts open when defaultOpen is true', async () => {
    mountSelect({
      options,
      defaultOpen: true,
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('[role="listbox"]')).not.toBeNull()
  })

  it('emits closeAutoFocus on close and honors preventDefault to preserve custom focus', async () => {
    let emittedEvent: Event | null = null
    const customButton = document.createElement('button')
    customButton.id = 'select-custom-focus'
    document.body.append(customButton)

    mountSelect({
      options,
      defaultOpen: true,
      onCloseAutoFocus: (e: Event) => {
        emittedEvent = e
        e.preventDefault()
        customButton.focus()
      },
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const listbox = document.body.querySelector('[role="listbox"]')
    expect(listbox).not.toBeNull()

    listbox?.dispatchEvent(
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

  it('renders default placeholder in en and de', async () => {
    const hostEn = mountSelect({ options }, undefined, 'en')
    const triggerEn = hostEn.querySelector('button')
    expect(triggerEn?.textContent).toContain('Select an option...')

    dispose()
    document.body.replaceChildren()

    const hostDe = mountSelect({ options }, undefined, 'de')
    const triggerDe = hostDe.querySelector('button')
    expect(triggerDe?.textContent).toContain('Option auswählen...')
  })

  it('preserves explicit placeholder overrides across locales, including empty strings', async () => {
    const hostEmpty = mountSelect({ options, placeholder: '' }, undefined, 'de')
    const triggerEmpty = hostEmpty.querySelector('button')
    expect(triggerEmpty?.textContent?.trim()).toBe('')
    expect(triggerEmpty?.textContent).not.toContain('Option auswählen...')

    dispose()
    document.body.replaceChildren()

    const hostCustom = mountSelect(
      { options, placeholder: 'Custom Site Selection' },
      undefined,
      'de',
    )
    const triggerCustom = hostCustom.querySelector('button')
    expect(triggerCustom?.textContent).toContain('Custom Site Selection')
  })
})
