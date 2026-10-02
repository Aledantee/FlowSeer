// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  createApp,
  defineComponent,
  h,
  nextTick,
  ref,
  type Component,
} from 'vue'
import { UiAppRoot, UiMotion as Motion } from '../index'

const UiMotion: Component = Motion

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

// motion's frame loop timestamps every frame from performance.now, so a
// controlled clock makes animation progress independent of host speed.
let motionClock = 0

function installMotionClock() {
  motionClock = 0
  vi.spyOn(performance, 'now').mockImplementation(() => motionClock)
}

async function advanceMotion(milliseconds: number) {
  motionClock += milliseconds
  // happy-dom implements requestAnimationFrame with setImmediate, so every
  // awaited macrotask turn runs frame-loop batches that read the advanced
  // timestamp. The batch count per turn is not fixed, so the callers below only
  // sample mid-flight progress, never an exact progress.
  for (let turn = 0; turn < 3; turn += 1) {
    await wait(0)
  }
}

describe('UiMotion', () => {
  it('uses the configured duration instead of the default spring', async () => {
    installMotionClock()
    const { target } = mountMotion({ animate: { x: 100 } })

    await advanceMotion(70)
    await advanceMotion(200)

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
                'data-render-count': renderCount.value,
                layout: true,
                layoutDependency: dependency.value,
              }),
              h(UiMotion, {
                as: 'div',
                class: 'position-box',
                'data-render-count': renderCount.value,
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
    installMotionClock()
    app.mount(host)
    dispose = () => {
      app.unmount()
      dispose = () => {}
    }

    await nextTick()
    await advanceMotion(20)
    const unchangedDependencyMeasurements = measurements.mock.calls.length
    host.querySelector<HTMLButtonElement>('.rerender-layout')?.click()
    await nextTick()
    await advanceMotion(20)
    expect(measurements.mock.calls.length).toBe(unchangedDependencyMeasurements)

    host.querySelector<HTMLButtonElement>('.toggle-layout')?.click()
    await nextTick()
    await advanceMotion(30)

    const layoutTransform =
      host.querySelector<HTMLElement>('.layout-box')?.style.transform
    const positionTransform =
      host.querySelector<HTMLElement>('.position-box')?.style.transform
    expect(layoutTransform).toContain('scale(')
    expect(positionTransform).toContain('translate')
    expect(positionTransform).not.toContain('scale(')
  })
})
