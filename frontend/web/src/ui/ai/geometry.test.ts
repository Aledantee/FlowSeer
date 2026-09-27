// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { placeAsk, visibleRect } from './geometry'
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

function rect(top: number, left: number, width: number, height: number): Rect {
  return {
    top,
    left,
    right: left + width,
    bottom: top + height,
    width,
    height,
  }
}

const mobile: Rect = {
  top: 0,
  left: 0,
  right: 390,
  bottom: 844,
  width: 390,
  height: 844,
}

const ask = { size: { width: 26, height: 22 }, gap: 2, inset: 1 }

function overlaps(placement: { top: number; left: number }, control: Rect) {
  return (
    placement.left < control.right &&
    placement.left + ask.size.width > control.left &&
    placement.top < control.bottom &&
    placement.top + ask.size.height > control.top
  )
}

describe('placeAsk', () => {
  const card = rect(593, 29, 332, 98)
  const previousRow = rect(494, 29, 332, 98)
  const nextRow = rect(692, 29, 332, 98)

  it('uses the inside top-right corner of the 390px adjacent-card case', () => {
    const placement = placeAsk({
      target: card,
      controls: [previousRow, nextRow],
      viewport: mobile,
      ...ask,
    })

    expect(placement).toEqual({ top: 595, left: 333 })
  })

  it('moves above the corner when a control occupies it', () => {
    const corner = rect(595, 333, 26, 22)
    const placement = placeAsk({
      target: card,
      controls: [nextRow, corner],
      viewport: mobile,
      ...ask,
    })

    expect(placement).toEqual({ top: 569, left: 333 })
    expect(overlaps(placement!, corner)).toBe(false)
  })

  it('returns undefined when every candidate overlaps a control', () => {
    const rightColumn = rect(560, 330, 60, 210)
    const leftColumn = rect(600, 0, 29, 80)
    const controls = [rightColumn, leftColumn]
    const placement = placeAsk({
      target: card,
      controls,
      viewport: mobile,
      ...ask,
    })

    expect(placement).toBeUndefined()
  })

  it('places or withholds Ask for every combination of obstacles', () => {
    // One obstacle per candidate region: inside the corner, then above it.
    const regions = [rect(595, 333, 26, 22), rect(569, 333, 26, 22)]
    for (let mask = 0; mask < 1 << regions.length; mask += 1) {
      const controls = regions.filter((_, index) => mask & (1 << index))
      const placement = placeAsk({
        target: card,
        controls,
        viewport: mobile,
        ...ask,
      })

      if (controls.length === regions.length) {
        expect(placement, `mask ${mask} blocked every region`).toBeUndefined()
        continue
      }

      expect(placement, `mask ${mask} left a clear region`).toBeDefined()
      expect(placement!.left).toBeGreaterThanOrEqual(ask.inset)
      expect(placement!.top).toBeGreaterThanOrEqual(ask.inset)
      expect(placement!.left + ask.size.width).toBeLessThanOrEqual(
        mobile.right - ask.inset,
      )
      expect(placement!.top + ask.size.height).toBeLessThanOrEqual(
        mobile.bottom - ask.inset,
      )
      for (const control of controls)
        expect(
          overlaps(placement!, control),
          `mask ${mask} overlaps ${JSON.stringify(control)}`,
        ).toBe(false)
    }
  })
})
