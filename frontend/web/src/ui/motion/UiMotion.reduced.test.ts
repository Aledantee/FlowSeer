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

describe('UiMotion reduced motion', () => {
  it('ends positional animation at once when the user prefers reduced motion', async () => {
    const target = mountMotion({ animate: { x: 100 } })

    await new Promise((resolve) => setTimeout(resolve, 40))

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
    app.mount(host)
    dispose = () => {
      app.unmount()
      dispose = () => {}
    }
    const target = host.querySelector('.reduced-motion-target')
    if (!(target instanceof HTMLElement))
      throw new Error('Missing reduced target')

    await nextTick()
    dependency.value = true
    await nextTick()
    await wait(40)

    expect(target.style.transform).not.toMatch(/scale\(|translate/)
  })
})
