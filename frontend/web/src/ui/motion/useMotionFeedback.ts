import {
  animate,
  useMotionConfig,
  useReducedMotion,
  type AnimationPlaybackControlsWithThen,
  type DOMKeyframesDefinition,
} from 'motion-v'
import { computed, onScopeDispose, watch } from 'vue'

const easing: [number, number, number, number] = [0.2, 0, 0, 1]

type AnimationWithChildren = AnimationPlaybackControlsWithThen & {
  animations?: Array<{
    animation?: {
      animation?: {
        finished?: Promise<unknown>
      }
    }
  }>
}

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
      restore: () => void
      timer: number
    }
  >()

  function cancel(element: HTMLElement) {
    const current = active.get(element)
    if (!current) return
    window.clearTimeout(current.timer)
    active.delete(element)
    current.animation.cancel()
    current.restore()
    queueMicrotask(() => {
      if (!active.has(element)) current.restore()
    })
    window.setTimeout(() => {
      if (!active.has(element)) current.restore()
    }, 0)
  }

  function clear() {
    for (const element of active.keys()) cancel(element)
  }

  function play(
    element: HTMLElement | undefined,
    keyframes: DOMKeyframesDefinition,
    duration = 0.14,
  ) {
    if (!element) return
    cancel(element)

    const originalOpacity = element.style.opacity
    const originalTransform = element.style.transform
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
    for (const child of (animation as AnimationWithChildren).animations ?? []) {
      void child.animation?.animation?.finished?.catch(() => {})
    }
    const finish = () => {
      if (active.get(element)?.animation === animation) cancel(element)
    }
    const timer = window.setTimeout(finish, duration * 1000 + 100)
    active.set(element, { animation, restore, timer })
    void animation.finished.then(finish).catch(() => {})
  }

  watch(userReducedMotion, clear)
  window.addEventListener('resize', clear)
  onScopeDispose(() => {
    clear()
    window.removeEventListener('resize', clear)
  })

  return { play, cancel, reduced }
}
