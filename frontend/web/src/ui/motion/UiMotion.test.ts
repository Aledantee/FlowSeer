// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import { UiAppRoot, UiMotion } from '../index'

let dispose = () => {}

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountMotion(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () =>
        h(UiMotion, {
          as: 'div',
          class: 'motion-target',
          ...props,
        }),
      )
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const target = host.querySelector('.motion-target')
  if (!(target instanceof HTMLElement)) throw new Error('Missing motion target')
  return { host, target }
}

function wait(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

describe('UiMotion', () => {
  it('uses the configured duration instead of the default spring', async () => {
    const { target } = mountMotion({ animate: { x: 100 } })

    await wait(250)

    expect(target.style.transform).toBe('translateX(100px)')
  })

  it('distinguishes size layout from position-only layout and measures only changed dependencies', async () => {
    const dependency = ref(false)
    const renderCount = ref(0)
    const measurements = vi
      .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockImplementation(function (this: HTMLElement) {
        const parent = this.closest('.layout-parent')
        const collapsed = parent?.classList.contains('collapsed')
        const width = collapsed ? 64 : 204
        const left = collapsed ? 40 : 0
        return {
          bottom: 80,
          height: 80,
          left,
          right: left + width,
          top: 0,
          width,
          x: 0,
          y: 0,
          toJSON: () => ({}),
        } as DOMRect
      })
    const Harness = defineComponent({
      setup() {
        return () =>
          h(
            'div',
            {
              class: dependency.value
                ? 'layout-parent collapsed'
                : 'layout-parent',
            },
            [
              h(UiMotion, {
                as: 'div',
                class: 'layout-box',
                layout: true,
                layoutDependency: dependency.value,
              }),
              h(UiMotion, {
                as: 'div',
                class: 'position-box',
                layout: 'position',
                layoutDependency: dependency.value,
              }),
              h(
                'button',
                {
                  class: 'toggle-layout',
                  onClick: () => {
                    dependency.value = !dependency.value
                  },
                },
                'toggle',
              ),
              h(
                'button',
                {
                  class: 'rerender-layout',
                  onClick: () => {
                    renderCount.value += 1
                  },
                },
                renderCount.value,
              ),
            ],
          )
      },
    })
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render() {
        return h(UiAppRoot, {}, () => h(Harness))
      },
    })
    app.mount(host)
    dispose = () => {
      app.unmount()
      dispose = () => {}
    }

    await nextTick()
    await wait(20)
    const unchangedDependencyMeasurements = measurements.mock.calls.length
    host.querySelector<HTMLButtonElement>('.rerender-layout')?.click()
    await nextTick()
    await wait(20)
    expect(measurements.mock.calls.length).toBe(unchangedDependencyMeasurements)

    host.querySelector<HTMLButtonElement>('.toggle-layout')?.click()
    await nextTick()
    await wait(30)

    const layoutTransform =
      host.querySelector<HTMLElement>('.layout-box')?.style.transform
    const positionTransform =
      host.querySelector<HTMLElement>('.position-box')?.style.transform
    expect(layoutTransform).toContain('scale(')
    expect(positionTransform).toContain('translate')
    expect(positionTransform).not.toContain('scale(')
  })
})
