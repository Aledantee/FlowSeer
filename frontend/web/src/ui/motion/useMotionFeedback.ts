import {
  animate,
  frame,
  useMotionConfig,
  useReducedMotion,
  type AnimationPlaybackControlsWithThen,
} from 'motion-v'
import { computed, onScopeDispose, watch } from 'vue'

const easing: [number, number, number, number] = [0.2, 0, 0, 1]
type MotionKey = 'opacity' | 'x' | 'y' | 'rotate' | 'scale'
type MotionKeyframes = Partial<Record<MotionKey, [number, number]>>

export function useMotionFeedback() {
  const config = useMotionConfig()
  const userReducedMotion = useReducedMotion()
  const reduced = computed(
    () =>
      config.value.reducedMotion === 'always' ||
      (config.value.reducedMotion === 'user' && userReducedMotion.value),
  )
  const active = new Map<
    HTMLElement,
    {
      animation: AnimationPlaybackControlsWithThen
      originalOpacity: string
      originalTransform: string
      restore: () => void
    }
  >()

  function cancel(element: HTMLElement) {
    const current = active.get(element)
    if (!current) return
    active.delete(element)

    // happy-dom rejects canceled Web Animations promises without a handler.
    for (const animation of element.getAnimations()) {
      void animation.finished.catch(() => {})
    }
    current.animation.cancel()
    frame.render(current.restore)
  }

  function clear() {
    for (const element of active.keys()) cancel(element)
  }

  function play(
    element: HTMLElement | undefined,
    keyframes: MotionKeyframes,
    duration = 0.14,
  ) {
    if (!element) return
    const current = active.get(element)
    const originalOpacity = current?.originalOpacity ?? element.style.opacity
    const originalTransform =
      current?.originalTransform ?? element.style.transform
    cancel(element)

    const restore = () => {
      element.style.opacity = originalOpacity
      element.style.transform = originalTransform
    }

    if (reduced.value) {
      if (keyframes.opacity === undefined) return
      keyframes = { opacity: keyframes.opacity }
    }

    const animation = animate(element, keyframes, {
      duration,
      ease: easing,
    })
    const finish = () => cancel(element)
    active.set(element, {
      animation,
      originalOpacity,
      originalTransform,
      restore,
    })
    void animation.finished.then(finish)
  }

  watch(userReducedMotion, clear)
  window.addEventListener('resize', clear)
  onScopeDispose(() => {
    clear()
    window.removeEventListener('resize', clear)
  })

  return { play, cancel, reduced }
}
