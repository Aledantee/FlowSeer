// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiInput from './UiInput.vue'
import UiTextarea from './UiTextarea.vue'
import UiCheckbox from './UiCheckbox.vue'
import UiSwitch from './UiSwitch.vue'
import UiRadioGroup from './UiRadioGroup.vue'
import UiSelect from './UiSelect.vue'

let cleanups: (() => void)[] = []
afterEach(() => {
  for (const cleanup of cleanups) {
    cleanup()
  }
  cleanups = []
  document.body.replaceChildren()
})

describe('form reset and uncontrolled contract', () => {
  it('resets controlled UiInput and updates v-model to value at mount', async () => {
    const model = ref('initial')
    const form = document.createElement('form')
    document.body.append(form)

    const app = createApp({
      render() {
        return h(UiInput, {
          modelValue: model.value,
          'onUpdate:modelValue': (v: string) => {
            model.value = v
          },
        })
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    const input = form.querySelector('input') as HTMLInputElement
    expect(input.value).toBe('initial')

    input.value = 'modified'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(model.value).toBe('modified')

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(input.value).toBe('initial')
    expect(model.value).toBe('initial')
  })

  it('preserves uncontrolled UiInput input across rerenders and resets to mount value', async () => {
    const count = ref(0)
    const form = document.createElement('form')
    document.body.append(form)

    const app = createApp({
      render() {
        return h('div', { 'data-count': count.value }, [
          h(UiInput, { name: 'uncontrolledField' }),
        ])
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    const input = form.querySelector('input') as HTMLInputElement
    input.value = 'user typed text'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()

    count.value++
    await nextTick()

    expect(input.value).toBe('user typed text')

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(input.value).toBe('')
  })

  it('resets controlled UiTextarea and updates v-model to value at mount', async () => {
    const model = ref('start note')
    const form = document.createElement('form')
    document.body.append(form)

    const app = createApp({
      render() {
        return h(UiTextarea, {
          modelValue: model.value,
          'onUpdate:modelValue': (v: string) => {
            model.value = v
          },
        })
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    const textarea = form.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea.value).toBe('start note')

    textarea.value = 'edited note'
    textarea.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(model.value).toBe('edited note')

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(textarea.value).toBe('start note')
    expect(model.value).toBe('start note')
  })

  it('resets uncontrolled UiCheckbox and keeps submitted FormData synchronized', async () => {
    const form = document.createElement('form')
    document.body.append(form)

    const app = createApp({
      render() {
        return h(UiCheckbox, { name: 'subscribe', value: 'yes' })
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    const button = form.querySelector('button') as HTMLButtonElement
    expect(button.getAttribute('data-state')).toBe('unchecked')
    expect(new FormData(form).get('subscribe')).toBeNull()

    button.click()
    await nextTick()

    expect(button.getAttribute('data-state')).toBe('checked')
    expect(new FormData(form).get('subscribe')).toBe('yes')

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(button.getAttribute('data-state')).toBe('unchecked')
    expect(new FormData(form).get('subscribe')).toBeNull()
  })

  it('resets controlled UiSwitch and updates v-model to value at mount', async () => {
    const active = ref(true)
    const form = document.createElement('form')
    document.body.append(form)

    const app = createApp({
      render() {
        return h(UiSwitch, {
          modelValue: active.value,
          'onUpdate:modelValue': (v: boolean) => {
            active.value = v
          },
        })
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    const button = form.querySelector('button') as HTMLButtonElement
    expect(button.getAttribute('data-state')).toBe('checked')

    button.click()
    await nextTick()

    expect(active.value).toBe(false)
    expect(button.getAttribute('data-state')).toBe('unchecked')

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(active.value).toBe(true)
    expect(button.getAttribute('data-state')).toBe('checked')
  })

  it('prefixes radio IDs with per-instance ID and tests two groups with same options', async () => {
    const container = document.createElement('div')
    document.body.append(container)

    const options = [
      { value: 'opt1', label: 'Option 1' },
      { value: 'opt2', label: 'Option 2' },
    ]

    const app = createApp({
      render() {
        return h('div', [
          h(UiRadioGroup, { options, name: 'group1' }),
          h(UiRadioGroup, { options, name: 'group2' }),
        ])
      },
    })
    app.mount(container)
    cleanups.push(() => app.unmount())

    const radioItems = container.querySelectorAll('[role="radio"]')
    expect(radioItems).toHaveLength(4)

    const ids = Array.from(radioItems).map((el) => el.id)
    const uniqueIds = new Set(ids)
    expect(uniqueIds.size).toBe(4)

    const labels = container.querySelectorAll('label')
    expect(labels).toHaveLength(4)
    for (let i = 0; i < 4; i++) {
      expect(labels[i].getAttribute('for')).toBe(ids[i])
    }
  })

  it('resets controlled UiRadioGroup to value at mount', async () => {
    const selected = ref('second')
    const form = document.createElement('form')
    document.body.append(form)

    const options = [
      { value: 'first', label: 'First' },
      { value: 'second', label: 'Second' },
    ]

    const app = createApp({
      render() {
        return h(UiRadioGroup, {
          modelValue: selected.value,
          options,
          'onUpdate:modelValue': (v: string) => {
            selected.value = v
          },
        })
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    const radios = form.querySelectorAll('[role="radio"]')
    const firstRadio = radios[0] as HTMLButtonElement

    firstRadio.click()
    await nextTick()
    expect(selected.value).toBe('first')

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(selected.value).toBe('second')
  })

  it('resets controlled UiSelect and updates v-model to value at mount', async () => {
    const selected = ref('opt-a')
    const form = document.createElement('form')
    document.body.append(form)

    const options = [
      { value: 'opt-a', label: 'Option A' },
      { value: 'opt-b', label: 'Option B' },
    ]

    const app = createApp({
      render() {
        return h(UiSelect, {
          modelValue: selected.value,
          options,
          'onUpdate:modelValue': (v: string) => {
            selected.value = v
          },
        })
      },
    })
    app.mount(form)
    cleanups.push(() => app.unmount())

    selected.value = 'opt-b'
    await nextTick()

    form.reset()
    await nextTick()
    await new Promise((r) => setTimeout(r, 10))

    expect(selected.value).toBe('opt-a')
  })

  it('clears visible state of UiCheckbox when controlled model changes to undefined', async () => {
    const checked = ref<boolean | undefined>(true)
    const host = document.createElement('div')
    document.body.append(host)

    const app = createApp({
      render() {
        return h(UiCheckbox, {
          modelValue: checked.value,
          'onUpdate:modelValue': (v: boolean | 'indeterminate') => {
            checked.value = v === true
          },
        })
      },
    })
    app.mount(host)
    cleanups.push(() => app.unmount())

    const button = host.querySelector('button') as HTMLButtonElement
    expect(button.getAttribute('data-state')).toBe('checked')

    checked.value = undefined
    await nextTick()

    expect(button.getAttribute('data-state')).toBe('unchecked')
  })

  it('clears visible state of UiSwitch when controlled model changes to undefined', async () => {
    const active = ref<boolean | undefined>(true)
    const host = document.createElement('div')
    document.body.append(host)

    const app = createApp({
      render() {
        return h(UiSwitch, {
          modelValue: active.value,
          'onUpdate:modelValue': (v: boolean) => {
            active.value = v
          },
        })
      },
    })
    app.mount(host)
    cleanups.push(() => app.unmount())

    const button = host.querySelector('button') as HTMLButtonElement
    expect(button.getAttribute('data-state')).toBe('checked')

    active.value = undefined
    await nextTick()

    expect(button.getAttribute('data-state')).toBe('unchecked')
  })

  it('clears visible state of UiRadioGroup when controlled model changes to undefined', async () => {
    const selected = ref<string | undefined>('opt1')
    const host = document.createElement('div')
    document.body.append(host)

    const options = [
      { value: 'opt1', label: 'Option 1' },
      { value: 'opt2', label: 'Option 2' },
    ]

    const app = createApp({
      render() {
        return h(UiRadioGroup, {
          modelValue: selected.value,
          options,
          'onUpdate:modelValue': (v: string) => {
            selected.value = v
          },
        })
      },
    })
    app.mount(host)
    cleanups.push(() => app.unmount())

    const radios = host.querySelectorAll('[role="radio"]')
    expect(radios[0].getAttribute('data-state')).toBe('checked')
    expect(radios[0].getAttribute('aria-checked')).toBe('true')

    selected.value = undefined
    await nextTick()

    expect(radios[0].getAttribute('data-state')).toBe('unchecked')
    expect(radios[0].getAttribute('aria-checked')).toBe('false')
    expect(radios[1].getAttribute('data-state')).toBe('unchecked')
    expect(radios[1].getAttribute('aria-checked')).toBe('false')
  })

  it('clears visible state of UiSelect when controlled model changes to undefined', async () => {
    const selected = ref<string | undefined>('opt-a')
    const host = document.createElement('div')
    document.body.append(host)

    const options = [
      { value: 'opt-a', label: 'Option A' },
      { value: 'opt-b', label: 'Option B' },
    ]

    const app = createApp({
      render() {
        return h(UiSelect, {
          modelValue: selected.value,
          options,
          placeholder: 'Select an option...',
          'onUpdate:modelValue': (v: string) => {
            selected.value = v
          },
        })
      },
    })
    app.mount(host)
    cleanups.push(() => app.unmount())

    const trigger = host.querySelector('button') as HTMLButtonElement
    expect(trigger.textContent).toContain('Option A')

    selected.value = undefined
    await nextTick()

    expect(trigger.textContent).toContain('Select an option...')
  })
})
