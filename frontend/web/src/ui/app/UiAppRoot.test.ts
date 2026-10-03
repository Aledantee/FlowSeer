// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { injectConfigProviderContext } from 'reka-ui'
import UiAppRoot from './UiAppRoot.vue'
import UiTooltip from '../tooltip/UiTooltip.vue'
import { createWebI18n } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

describe('UiAppRoot', () => {
  it('observes Rekas injected locale changing en to de after a Composer update', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const i18n = createWebI18n('en')

    const Consumer = defineComponent({
      setup() {
        const rekaContext = injectConfigProviderContext()
        return () =>
          h('div', { class: 'reka-locale' }, rekaContext.locale?.value)
      },
    })

    const app = createApp({
      render() {
        return h(UiAppRoot, {}, () => h(Consumer))
      },
    })
    app.use(i18n)
    app.mount(host)
    dispose = () => {
      app.unmount()
      dispose = () => {}
    }

    const element = host.querySelector('.reka-locale')
    expect(element?.textContent).toBe('en')

    i18n.global.locale.value = 'de'
    await nextTick()
    expect(element?.textContent).toBe('de')
  })

  it('provides tooltip configuration so a child tooltip opens after the provider delay', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const i18n = createWebI18n()

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
    app.use(i18n)
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
