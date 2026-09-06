import { onUnmounted } from 'vue'
import { animate } from 'motion/mini'

export function useMotionFeedback() {
  const preference = window.matchMedia('(prefers-reduced-motion: reduce)')
  const active = new Map<
    HTMLElement,
    {
      animation: ReturnType<typeof animate>
      restore: () => void
    }
  >()
  function cancel(element: HTMLElement) {
    const current = active.get(element)
    current?.animation.cancel()
    current?.restore()
    active.delete(element)
  }
  function clear() {
    for (const element of active.keys()) cancel(element)
  }
  function play(
    element: HTMLElement | undefined,
    keyframes: Parameters<typeof animate>[1],
    duration = 0.14,
  ) {
    if (!element) return
    cancel(element)
    if (preference.matches) return
    const original = Object.keys(keyframes).map((key) => {
      const property = key.replace(
        /[A-Z]/g,
        (letter) => `-${letter.toLowerCase()}`,
      )
      return {
        property,
        value: element.style.getPropertyValue(property),
        priority: element.style.getPropertyPriority(property),
      }
    })
    const restore = () => {
      for (const { property, value, priority } of original) {
        if (value) element.style.setProperty(property, value, priority)
        else element.style.removeProperty(property)
      }
    }
    const animation = animate(element, keyframes, {
      duration,
      ease: [0.2, 0, 0, 1],
    })
    active.set(element, { animation, restore })
    // Release animation styles so responsive CSS remains the source of truth.
    void animation.then(() => {
      if (active.get(element)?.animation === animation) cancel(element)
    })
  }
  preference.addEventListener('change', clear)
  window.addEventListener('resize', clear)
  onUnmounted(() => {
    clear()
    preference.removeEventListener('change', clear)
    window.removeEventListener('resize', clear)
  })
  return { play, cancel }
}
