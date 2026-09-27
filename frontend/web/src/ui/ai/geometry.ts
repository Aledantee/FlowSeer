// Where a registered element actually shows on screen. The affordance is
// drawn over the element, so it must follow the element as panes move and
// hide when the element is scrolled out of its container.

export interface Rect {
  top: number
  right: number
  bottom: number
  left: number
  width: number
  height: number
}

export interface Size {
  width: number
  height: number
}

export interface Point {
  top: number
  left: number
}

function toRect(rect: DOMRect | Rect): Rect {
  return {
    top: rect.top,
    right: rect.right,
    bottom: rect.bottom,
    left: rect.left,
    width: rect.width,
    height: rect.height,
  }
}

export function intersect(a: Rect, b: Rect): Rect {
  const top = Math.max(a.top, b.top)
  const left = Math.max(a.left, b.left)
  const right = Math.min(a.right, b.right)
  const bottom = Math.min(a.bottom, b.bottom)
  return {
    top,
    left,
    right,
    bottom,
    width: Math.max(0, right - left),
    height: Math.max(0, bottom - top),
  }
}

function viewportRect(): Rect {
  return {
    top: 0,
    left: 0,
    right: window.innerWidth,
    bottom: window.innerHeight,
    width: window.innerWidth,
    height: window.innerHeight,
  }
}

const clippingOverflow = new Set(['hidden', 'auto', 'scroll', 'clip'])

function clippingAncestors(element: HTMLElement): HTMLElement[] {
  const found: HTMLElement[] = []
  for (
    let node = element.parentElement;
    node && node !== document.body;
    node = node.parentElement
  ) {
    const style = getComputedStyle(node)
    if (
      [style.overflow, style.overflowX, style.overflowY].some((overflow) =>
        clippingOverflow.has(overflow),
      )
    )
      found.push(node)
  }
  return found
}

// The part of the element inside its scroll containers and the window, or
// undefined when nothing of it is visible.
export function visibleRect(
  element: HTMLElement,
  viewport: Rect = viewportRect(),
): Rect | undefined {
  const box = toRect(element.getBoundingClientRect())
  if (box.width <= 0 && box.height <= 0) return undefined
  let clip = viewport
  for (const ancestor of clippingAncestors(element))
    clip = intersect(clip, toRect(ancestor.getBoundingClientRect()))
  const visible = intersect(box, clip)
  if (visible.width <= 0 || visible.height <= 0) return undefined
  return visible
}

export interface AskPlacement {
  target: Rect
  controls: readonly Rect[]
  viewport: Rect
  size: Size
  gap: number
  inset: number
}

function clearsControls(
  point: Point,
  size: Size,
  viewport: Rect,
  controls: readonly Rect[],
  inset: number,
): boolean {
  if (
    point.left < inset ||
    point.top < inset ||
    point.left + size.width > viewport.right - inset ||
    point.top + size.height > viewport.bottom - inset
  )
    return false
  return controls.every(
    (control) =>
      point.left + size.width <= control.left ||
      point.left >= control.right ||
      point.top + size.height <= control.top ||
      point.top >= control.bottom,
  )
}

// Picks where the Ask affordance sits on its target: inside the target's
// top-right corner, or just above that corner when a control occupies it. The
// first spot inside the inset viewport that overlaps no measured control wins.
// Undefined means neither spot is both on screen and clear, and a caller that
// renders nothing for undefined keeps Ask from covering a control when a
// crowded page leaves no room.
export function placeAsk({
  target,
  controls,
  viewport,
  size,
  gap,
  inset,
}: AskPlacement): Point | undefined {
  const left = Math.min(
    Math.max(target.right - gap - size.width, inset),
    viewport.right - inset - size.width,
  )
  const candidates: Point[] = [
    { top: target.top + gap, left },
    { top: target.top - gap - size.height, left },
  ]
  return candidates.find((candidate) =>
    clearsControls(candidate, size, viewport, controls, inset),
  )
}
