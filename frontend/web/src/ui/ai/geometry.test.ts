// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { visibleRect } from './geometry'
import type { Rect } from './geometry'

function setBox(element: HTMLElement, box: Rect) {
  element.getBoundingClientRect = () =>
    ({
      x: box.left,
      y: box.top,
      ...box,
      toJSON: () => ({}),
    }) as DOMRect
}

function setDimensions(
  element: HTMLElement,
  dimensions: {
    scrollHeight: number
    scrollWidth: number
    clientHeight: number
    clientWidth: number
  },
) {
  for (const [name, value] of Object.entries(dimensions))
    Object.defineProperty(element, name, { configurable: true, value })
}

const viewport: Rect = {
  top: 0,
  right: 200,
  bottom: 200,
  left: 0,
  width: 200,
  height: 200,
}

describe('visibleRect', () => {
  it.each(['hidden', 'auto', 'scroll', 'clip'])(
    'clips to an overflow-%s ancestor even without scrollable dimensions',
    (overflow) => {
      const ancestor = document.createElement('div')
      ancestor.style.overflow = overflow
      const target = document.createElement('div')
      ancestor.append(target)
      document.body.append(ancestor)
      setBox(ancestor, {
        top: 20,
        right: 100,
        bottom: 100,
        left: 20,
        width: 80,
        height: 80,
      })
      setBox(target, {
        top: 80,
        right: 120,
        bottom: 120,
        left: 80,
        width: 40,
        height: 40,
      })
      setDimensions(ancestor, {
        scrollHeight: 80,
        scrollWidth: 80,
        clientHeight: 80,
        clientWidth: 80,
      })

      expect(visibleRect(target, viewport)).toEqual({
        top: 80,
        right: 100,
        bottom: 100,
        left: 80,
        width: 20,
        height: 20,
      })
    },
  )

  it('does not clip to an overflow-visible ancestor with excess scroll dimensions', () => {
    const ancestor = document.createElement('div')
    ancestor.style.overflow = 'visible'
    const target = document.createElement('div')
    ancestor.append(target)
    document.body.append(ancestor)
    setBox(ancestor, {
      top: 20,
      right: 100,
      bottom: 100,
      left: 20,
      width: 80,
      height: 80,
    })
    setBox(target, {
      top: 80,
      right: 120,
      bottom: 120,
      left: 80,
      width: 40,
      height: 40,
    })
    setDimensions(ancestor, {
      scrollHeight: 160,
      scrollWidth: 160,
      clientHeight: 80,
      clientWidth: 80,
    })

    expect(visibleRect(target, viewport)).toEqual({
      top: 80,
      right: 120,
      bottom: 120,
      left: 80,
      width: 40,
      height: 40,
    })
  })
})
