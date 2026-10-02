import {
  animateMini,
  useMotionConfig,
  useReducedMotion,
  type AnimationPlaybackControlsWithThen,
} from 'motion-v'
import { computed, onScopeDispose, watch } from 'vue'

const easing: [number, number, number, number] = [0.2, 0, 0, 1]
const transformKeys = ['x', 'y', 'rotate', 'scale'] as const
type MotionKey = 'opacity' | (typeof transformKeys)[number]
type MotionKeyframes = Partial<Record<MotionKey, [number, number]>>
type OwnedStyles = Partial<Record<'opacity' | 'transform', string>>
type CompiledKeyframes = Partial<
  Record<'opacity' | 'transform', [number | string, number | string]>
>

interface ActiveAnimation {
  animation: AnimationPlaybackControlsWithThen
  nativeAnimations: Animation[]
  ownedStyles: OwnedStyles
  restore: () => void
}

function hasTransformKey(keyframes: MotionKeyframes) {
  return transformKeys.some((key) => keyframes[key] !== undefined)
}

function transformValue(key: (typeof transformKeys)[number], value: number) {
  const name = key === 'x' ? 'translateX' : key === 'y' ? 'translateY' : key
  const unit = key === 'scale' ? '' : key === 'rotate' ? 'deg' : 'px'
  return `${name}(${value}${unit})`
}

function fillTransformKeyframes(
  keyframes: MotionKeyframes,
): [string, string] | undefined {
  if (!hasTransformKey(keyframes)) return undefined
  const endpoint = (index: 0 | 1) =>
    transformKeys
      .flatMap((key) => {
        const pair = keyframes[key]
        return pair === undefined ? [] : [transformValue(key, pair[index])]
      })
      .join(' ')
  return [endpoint(0), endpoint(1)]
}

function compileKeyframes(
  keyframes: MotionKeyframes | undefined,
  reduced: boolean,
) {
  if (!keyframes) return {}
  const compiled: CompiledKeyframes = {}
  if (keyframes.opacity !== undefined) compiled.opacity = keyframes.opacity
  if (!reduced) {
    const transform = fillTransformKeyframes(keyframes)
    if (transform) compiled.transform = transform
  }
  return compiled
}

function hasEffectiveKeyframes(keyframes: CompiledKeyframes) {
  return Object.keys(keyframes).length > 0
}

export function useMotionFeedback() {
  const config = useMotionConfig()
  const userReducedMotion = useReducedMotion()
  const reduced = computed(
    () =>
      config.value.reducedMotion === 'always' ||
      (config.value.reducedMotion === 'user' && userReducedMotion.value),
  )
  const active = new Map<HTMLElement, ActiveAnimation>()

  function stop(element: HTMLElement) {
    const current = active.get(element)
    if (!current) return
    active.delete(element)

    // Native cancellation rejects finished promises in happy-dom and browsers.
    for (const animation of current.nativeAnimations) {
      void animation.finished.catch(() => {})
    }
    void current.animation.finished.catch(() => {})
    current.animation.cancel()
    current.restore()
  }

  function cancel(element: HTMLElement) {
    stop(element)
  }

  function clear() {
    for (const element of [...active.keys()]) cancel(element)
  }

  function play(
    element: HTMLElement | undefined,
    keyframes?: MotionKeyframes,
    duration = 0.14,
  ) {
    if (!element) return

    const current = active.get(element)
    const compiled = compileKeyframes(keyframes, reduced.value)
    if (!hasEffectiveKeyframes(compiled)) {
      cancel(element)
      return
    }

    if (current) stop(element)

    const ownedStyles: OwnedStyles = {}
    if (compiled.opacity !== undefined) {
      ownedStyles.opacity = element.style.opacity
    }
    if (compiled.transform !== undefined) {
      ownedStyles.transform = element.style.transform
    }
    const restore = () => {
      if (ownedStyles.opacity !== undefined)
        element.style.opacity = ownedStyles.opacity
      if (ownedStyles.transform !== undefined)
        element.style.transform = ownedStyles.transform
    }

    const previousAnimations = new Set(element.getAnimations())
    const animation = animateMini([element], compiled, {
      duration,
      ease: easing,
    })
    const nativeAnimations = element
      .getAnimations()
      .filter((nativeAnimation) => !previousAnimations.has(nativeAnimation))
    for (const nativeAnimation of nativeAnimations) {
      void nativeAnimation.finished.catch(() => {})
    }

    active.set(element, {
      animation,
      nativeAnimations,
      ownedStyles,
      restore,
    })

    const finish = () => {
      if (active.get(element)?.animation !== animation) return
      stop(element)
    }
    void animation.finished.then(finish).catch(() => {})
  }

  watch(() => [config.value.reducedMotion, userReducedMotion.value], clear)
  window.addEventListener('resize', clear)
  onScopeDispose(() => {
    clear()
    window.removeEventListener('resize', clear)
  })

  return { play, cancel, reduced }
}
