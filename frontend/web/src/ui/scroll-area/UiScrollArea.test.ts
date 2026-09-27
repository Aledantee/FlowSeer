// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiScrollArea from './UiScrollArea.vue'

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

describe('UiScrollArea', () => {
  it('renders content inside scroll viewport', () => {
    const host = mountApp(() =>
      h(
        UiScrollArea,
        {},
        {
          default: () => h('div', { id: 'scroll-content' }, 'Scrollable Text'),
        },
      ),
    )

    const content = host.querySelector('#scroll-content')
    expect(content).not.toBeNull()
    expect(content?.textContent).toBe('Scrollable Text')
  })

  it('exposes element reference pointing to the scrollable HTML element', async () => {
    const scrollAreaRef = ref<InstanceType<typeof UiScrollArea> | null>(null)
    mountApp(() =>
      h(
        UiScrollArea,
        {
          ref: scrollAreaRef,
        },
        {
          default: () => h('div', 'Content'),
        },
      ),
    )

    await nextTick()
    expect(scrollAreaRef.value).not.toBeNull()
    expect(scrollAreaRef.value?.element).toBeDefined()
    expect(scrollAreaRef.value?.element instanceof HTMLElement).toBe(true)
  })

  it('vertical scrollbar renders for axis="y" or axis="both"', async () => {
    const host = mountApp(() =>
      h(
        UiScrollArea,
        {
          axis: 'y',
          type: 'always',
        },
        {
          default: () => h('div', 'Tall content for scrollbar check'),
        },
      ),
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const scrollbars = host.querySelectorAll('[data-orientation="vertical"]')
    expect(scrollbars.length).toBeGreaterThan(0)
  })
})
