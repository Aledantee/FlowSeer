// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => {
    return Object.assign(new EventTarget(), {
      matches: query.includes('reduce'),
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
    })
  })
})

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function mountMotion(props: Record<string, unknown>) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () =>
        h(UiMotion, {
          as: 'div',
          class: 'reduced-motion-target',
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
  const target = host.querySelector('.reduced-motion-target')
  if (!(target instanceof HTMLElement))
    throw new Error('Missing reduced target')
  return target
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
  // happy-dom implements requestAnimationFrame with setImmediate, so an
  // awaited macrotask turn lets a pending frame-loop batch read the advanced
  // timestamp. A batch runs only while one is scheduled
  // (motion-dom/dist/es/frameloop/batcher.mjs:43-54), and the batch count per
  // turn is not fixed, so the callers below read whatever progress the clock
  // has reached, mid-flight or final.
  for (let turn = 0; turn < 3; turn += 1) {
    await wait(0)
  }
}

describe('UiMotion reduced motion', () => {
  it('ends positional animation at once when the user prefers reduced motion', async () => {
    installMotionClock()
    const target = mountMotion({ animate: { x: 100 } })

    await advanceMotion(70)

    expect(target.style.transform).toBe('translateX(100px)')
  })

  it('ends layout animation at once when the user prefers reduced motion', async () => {
    const dependency = ref(false)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
      function (this: HTMLElement) {
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
          x: left,
          y: 0,
          toJSON: () => ({}),
        } as DOMRect
      },
    )
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
            h(UiMotion, {
              as: 'div',
              class: 'reduced-motion-target',
              layout: true,
              layoutDependency: dependency.value,
            }),
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
    const target = host.querySelector('.reduced-motion-target')
    if (!(target instanceof HTMLElement))
      throw new Error('Missing reduced target')

    await nextTick()
    await advanceMotion(20)
    dependency.value = true
    await nextTick()
    await advanceMotion(40)

    expect(target.style.transform).not.toMatch(/scale\(|translate/)
  })
})
