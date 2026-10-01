// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { UiAppRoot, UiMotion } from '../index'

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

describe('UiMotion reduced motion', () => {
  it('ends positional animation at once when the user prefers reduced motion', async () => {
    const target = mountMotion({ animate: { x: 100 } })

    await new Promise((resolve) => setTimeout(resolve, 40))

    expect(target.style.transform).toBe('translateX(100px)')
  })

  it('ends layout animation at once when the user prefers reduced motion', async () => {
    const target = mountMotion({ layout: true, layoutDependency: 1 })

    await nextTick()

    expect(target.style.transform).toBe('')
  })
})
