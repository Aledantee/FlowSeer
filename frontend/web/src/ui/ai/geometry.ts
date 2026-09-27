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

// The nearest ancestors that scroll their content, nearest first.
export function scrollableAncestors(element: HTMLElement): HTMLElement[] {
  const found: HTMLElement[] = []
  for (
    let node = element.parentElement;
    node && node !== document.body;
    node = node.parentElement
  ) {
    if (
      node.scrollHeight > node.clientHeight + 1 ||
      node.scrollWidth > node.clientWidth + 1
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
  for (const ancestor of scrollableAncestors(element))
    clip = intersect(clip, toRect(ancestor.getBoundingClientRect()))
  const visible = intersect(box, clip)
  if (visible.width <= 0 || visible.height <= 0) return undefined
  return visible
}
