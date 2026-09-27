// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h } from 'vue'
import UiSkeleton from './UiSkeleton.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountSkeleton(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiSkeleton, props)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiSkeleton', () => {
  it('applies variant classes and pulse animation', () => {
    const textSkeleton = mountSkeleton({ variant: 'text' })
    expect(textSkeleton.el.className).toContain('animate-pulse')
    expect(textSkeleton.el.className).toContain('rounded-control')
    dispose()

    const circularSkeleton = mountSkeleton({
      variant: 'circular',
      width: 40,
      height: 40,
    })
    expect(circularSkeleton.el.className).toContain('rounded-full')
    expect(circularSkeleton.el.style.width).toBe('40px')
    expect(circularSkeleton.el.style.height).toBe('40px')
    dispose()

    const rectangularSkeleton = mountSkeleton({
      variant: 'rectangular',
      width: '100%',
      height: 120,
    })
    expect(rectangularSkeleton.el.className).toContain('rounded-control')
    expect(rectangularSkeleton.el.style.width).toBe('100%')
    expect(rectangularSkeleton.el.style.height).toBe('120px')
    dispose()
  })
})
