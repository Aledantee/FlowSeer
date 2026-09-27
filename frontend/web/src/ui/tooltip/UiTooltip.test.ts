// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { TooltipProvider } from 'reka-ui'
import UiTooltip from './UiTooltip.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountTooltip(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(TooltipProvider, {}, () =>
        h(
          UiTooltip,
          {
            label: 'Quick action',
            hint: 'Shortcut hint',
            delayDuration: 0,
            ...props,
          },
          {
            default: () =>
              h('button', { class: 'custom-target-button' }, 'Target Button'),
          },
        ),
      )
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const trigger = host.querySelector('button.custom-target-button')
  if (!trigger) throw new Error('Missing trigger button')
  return { host, trigger: trigger as HTMLButtonElement }
}

describe('UiTooltip', () => {
  it('renders trigger child directly and displays content on hover', async () => {
    const { trigger } = mountTooltip()
    expect(trigger.textContent).toBe('Target Button')

    trigger.dispatchEvent(
      new PointerEvent('pointermove', {
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    const content = document.body.textContent
    expect(content).toContain('Quick action')
    expect(content).toContain('Shortcut hint')
  })

  it('displays content on keyboard focus and fails if focus handling is absent', async () => {
    const { trigger } = mountTooltip()

    expect(document.body.textContent).not.toContain('Quick action')
    expect(document.body.textContent).not.toContain('Shortcut hint')

    trigger.dispatchEvent(
      new FocusEvent('focus', {
        bubbles: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(document.body.textContent).toContain('Quick action')
    expect(document.body.textContent).toContain('Shortcut hint')
  })
})
