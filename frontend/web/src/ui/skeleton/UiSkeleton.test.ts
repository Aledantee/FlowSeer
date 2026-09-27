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

  it('retains default height when only width is supplied', () => {
    const textSkeleton = mountSkeleton({ variant: 'text', width: '80%' })
    expect(textSkeleton.el.className).toContain('h-4')
    expect(textSkeleton.el.className).not.toContain('w-full')
    expect(textSkeleton.el.style.width).toBe('80%')
    dispose()

    const circularSkeleton = mountSkeleton({ variant: 'circular', width: 48 })
    expect(circularSkeleton.el.className).toContain('h-10')
    expect(circularSkeleton.el.className).not.toContain('w-10')
    expect(circularSkeleton.el.style.width).toBe('48px')
    dispose()

    const rectangularSkeleton = mountSkeleton({
      variant: 'rectangular',
      width: '60%',
    })
    expect(rectangularSkeleton.el.className).toContain('h-24')
    expect(rectangularSkeleton.el.className).not.toContain('w-full')
    expect(rectangularSkeleton.el.style.width).toBe('60%')
    dispose()
  })

  it('retains default width when only height is supplied', () => {
    const textSkeleton = mountSkeleton({ variant: 'text', height: 20 })
    expect(textSkeleton.el.className).toContain('w-full')
    expect(textSkeleton.el.className).not.toContain('h-4')
    expect(textSkeleton.el.style.height).toBe('20px')
    dispose()

    const circularSkeleton = mountSkeleton({ variant: 'circular', height: 48 })
    expect(circularSkeleton.el.className).toContain('w-10')
    expect(circularSkeleton.el.className).not.toContain('h-10')
    expect(circularSkeleton.el.style.height).toBe('48px')
    dispose()

    const rectangularSkeleton = mountSkeleton({
      variant: 'rectangular',
      height: 100,
    })
    expect(rectangularSkeleton.el.className).toContain('w-full')
    expect(rectangularSkeleton.el.className).not.toContain('h-24')
    expect(rectangularSkeleton.el.style.height).toBe('100px')
    dispose()
  })

  it('treats numeric zero as an explicit dimension', () => {
    const zeroWidth = mountSkeleton({ variant: 'text', width: 0 })
    expect(zeroWidth.el.style.width).toBe('0px')
    expect(zeroWidth.el.className).not.toContain('w-full')
    expect(zeroWidth.el.className).toContain('h-4')
    dispose()

    const zeroHeight = mountSkeleton({ variant: 'text', height: 0 })
    expect(zeroHeight.el.style.height).toBe('0px')
    expect(zeroHeight.el.className).not.toContain('h-4')
    expect(zeroHeight.el.className).toContain('w-full')
    dispose()
  })
})
