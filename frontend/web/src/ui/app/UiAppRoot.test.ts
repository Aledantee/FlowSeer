// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiAppRoot from './UiAppRoot.vue'
import UiTooltip from '../tooltip/UiTooltip.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

describe('UiAppRoot', () => {
  it('provides tooltip configuration so a child tooltip opens after the provider delay', async () => {
    const host = document.createElement('div')
    document.body.append(host)

    const app = createApp({
      render() {
        return h(UiAppRoot, {}, () =>
          h(
            UiTooltip,
            { label: 'Network status' },
            {
              default: () =>
                h('button', { class: 'network-trigger' }, 'Trigger'),
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

    const trigger = host.querySelector('button.network-trigger')
    if (!trigger) throw new Error('Missing trigger button')

    trigger.dispatchEvent(
      new PointerEvent('pointermove', {
        bubbles: true,
        cancelable: true,
      }),
    )

    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 100))
    await nextTick()
    expect(document.body.textContent).not.toContain('Network status')

    await new Promise((resolve) => setTimeout(resolve, 300))
    await nextTick()
    expect(document.body.textContent).toContain('Network status')
  })
})
