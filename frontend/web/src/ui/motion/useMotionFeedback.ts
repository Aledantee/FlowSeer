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
type TransformKey = Exclude<MotionKey, 'opacity'>
type MotionKeyframes = Partial<Record<MotionKey, [number, number]>>
const transformKeys = ['x', 'y', 'rotate', 'scale'] as const

function hasTransformKey(keyframes: MotionKeyframes) {
  return transformKeys.some((key) => keyframes[key] !== undefined)
}

function fillTransformKeyframes(keyframes: MotionKeyframes) {
  const filled = { ...keyframes }
  for (const key of transformKeys) {
    filled[key] ??= key === 'scale' ? [1, 1] : [0, 0]
  }
  return filled
}

function firstTransform(keyframes: MotionKeyframes) {
  const names: Record<TransformKey, string> = {
    x: 'translateX',
    y: 'translateY',
    rotate: 'rotate',
    scale: 'scale',
  }
  const values = transformKeys.flatMap((key) => {
    const value = keyframes[key]?.[0]
    if (value === undefined) return []
    const isDefault = value === (key === 'scale' ? 1 : 0)
    if (isDefault) return []
    const unit = key === 'scale' ? '' : key === 'rotate' ? 'deg' : 'px'
    return `${names[key]}(${value}${unit})`
  })
  return values.join(' ') || 'none'
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
      originalOpacity: string
      originalTransform: string
      restore: () => void
    }
  >()

  function stop(element: HTMLElement, restore: boolean) {
    const current = active.get(element)
    if (!current) return
    active.delete(element)

    // happy-dom rejects canceled Web Animations promises without a handler.
    for (const animation of element.getAnimations()) {
      void animation.finished.catch(() => {})
    }
    current.animation.cancel()
    if (restore) frame.render(current.restore)
  }

  function cancel(element: HTMLElement) {
    stop(element, true)
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
    let playKeyframes = keyframes
    if (reduced.value) {
      if (keyframes.opacity === undefined) {
        cancel(element)
        return
      }
      playKeyframes = { opacity: keyframes.opacity }
    } else if (hasTransformKey(keyframes)) {
      // motion-dom retains unmentioned transform values in its per-element store.
      playKeyframes = fillTransformKeyframes(keyframes)
    }

    if (current) {
      stop(element, false)
      if (!hasTransformKey(playKeyframes)) {
        frame.postRender(() => {
          element.style.transform = originalTransform
        })
      }
    }

    const restore = () => {
      element.style.opacity = originalOpacity
      element.style.transform = originalTransform
    }

    const animation = animate(element, playKeyframes, {
      duration,
      ease: easing,
    })
    if (current) {
      // A canceled animation writes its first keyframe on the next render.
      frame.postRender(() => {
        if (playKeyframes.opacity) {
          element.style.opacity = String(playKeyframes.opacity[0])
        }
        if (hasTransformKey(playKeyframes)) {
          element.style.transform = firstTransform(playKeyframes)
        }
      })
    }
    const finish = () => {
      // A stale completion must not cancel the animation that replaced it.
      if (active.get(element)?.animation !== animation) return
      cancel(element)
    }
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
