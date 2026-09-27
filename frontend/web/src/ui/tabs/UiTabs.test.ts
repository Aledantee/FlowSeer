// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiTabs from './UiTabs.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

const sampleTabs = [
  { value: 'tab1', label: 'Tab 1', content: 'Content 1' },
  { value: 'tab2', label: 'Tab 2', content: 'Content 2' },
  { value: 'tab3', label: 'Tab 3', disabled: true, content: 'Content 3' },
]

function mountTabs(props: () => Record<string, unknown>) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiTabs, {
        tabs: sampleTabs,
        ...props(),
      })
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiTabs', () => {
  it('tab trigger activates corresponding tab panel content', async () => {
    const active = ref('tab1')
    const host = mountTabs(() => ({
      modelValue: active.value,
      'onUpdate:modelValue': (v: string) => {
        active.value = v
      },
    }))

    await nextTick()
    expect(host.textContent).toContain('Content 1')

    const triggers = host.querySelectorAll<HTMLButtonElement>('[role="tab"]')
    expect(triggers.length).toBe(3)

    triggers[1]?.dispatchEvent(
      new MouseEvent('mousedown', {
        bubbles: true,
        cancelable: true,
        button: 0,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(active.value).toBe('tab2')
  })

  it('arrow keys navigate tab triggers', async () => {
    const host = mountTabs(() => ({
      defaultValue: 'tab1',
    }))

    await nextTick()
    const triggers = host.querySelectorAll<HTMLButtonElement>('[role="tab"]')

    triggers[0]?.focus()
    triggers[0]?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'ArrowRight',
        code: 'ArrowRight',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(
      triggers[1]?.hasAttribute('data-state') &&
        triggers[1]?.getAttribute('data-state') === 'active',
    ).toBe(true)
  })

  it('disabled tab cannot be activated', async () => {
    const active = ref('tab1')
    const host = mountTabs(() => ({
      modelValue: active.value,
      'onUpdate:modelValue': (v: string) => {
        active.value = v
      },
    }))

    await nextTick()
    const triggers = host.querySelectorAll<HTMLButtonElement>('[role="tab"]')
    const disabledTab = triggers[2]

    expect(disabledTab?.hasAttribute('disabled')).toBe(true)

    disabledTab?.dispatchEvent(
      new MouseEvent('mousedown', {
        bubbles: true,
        cancelable: true,
        button: 0,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(active.value).toBe('tab1')
  })
})
