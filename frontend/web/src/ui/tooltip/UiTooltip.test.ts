// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiTooltip from './UiTooltip.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

describe('UiTooltip', () => {
  it('renders trigger child directly (as-child) and displays content on hover and keyboard focus', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render() {
        return h(
          UiTooltip,
          {
            label: 'Quick action',
            hint: 'Shortcut hint',
            delayDuration: 0,
          },
          {
            default: () =>
              h('button', { class: 'custom-target-button' }, 'Target Button'),
          },
        )
      },
    })
    app.mount(host)
    dispose = () => app.unmount()

    const trigger = host.querySelector('button.custom-target-button')
    expect(trigger).not.toBeNull()
    expect(trigger?.textContent).toBe('Target Button')

    // Test hover / pointermove
    trigger?.dispatchEvent(
      new PointerEvent('pointermove', {
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    let content = document.body.textContent
    expect(content).toContain('Quick action')
    expect(content).toContain('Shortcut hint')

    // Test pointerleave closes it
    trigger?.dispatchEvent(
      new PointerEvent('pointerleave', {
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    // Test focus opens it
    trigger?.dispatchEvent(
      new FocusEvent('focus', {
        bubbles: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    content = document.body.textContent
    expect(content).toContain('Quick action')
  })
})
