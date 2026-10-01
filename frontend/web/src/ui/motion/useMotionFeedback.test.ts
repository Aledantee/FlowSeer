// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import { UiAppRoot } from '../index'
import { UiMotionConfig } from './index'
import { useMotionFeedback } from './useMotionFeedback'

vi.hoisted(() => {
  vi.stubGlobal(
    'requestAnimationFrame',
    (callback: (timestamp: number) => void) =>
      setTimeout(() => callback(performance.now()), 16),
  )
})

interface MountOptions {
  reducedMotion?: 'always' | 'never'
  targetCount?: number
}

interface MountedFeedback {
  element: HTMLElement
  elements: HTMLElement[]
  feedback: ReturnType<typeof useMotionFeedback>
  reducedMotion: ReturnType<typeof ref<'always' | 'never' | undefined>>
}

let dispose = () => {}
let preference: EventTarget & { matches: boolean }

function createPreference(matches: boolean) {
  return Object.assign(new EventTarget(), {
    matches,
    media: '(prefers-reduced-motion: reduce)',
    onchange: null,
    addListener() {},
    removeListener() {},
  }) as EventTarget & { matches: boolean }
}

beforeEach(() => {
  preference = createPreference(false)
  vi.stubGlobal('matchMedia', () => preference)
})

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})

function mountFeedback(options: MountOptions = {}): MountedFeedback {
  let feedback: ReturnType<typeof useMotionFeedback> | undefined
  const reducedMotion = ref<'always' | 'never' | undefined>(
    options.reducedMotion,
  )
  const Harness = defineComponent({
    setup() {
      feedback = useMotionFeedback()
      return () =>
        h(
          'div',
          { class: 'feedback-targets' },
          Array.from({ length: options.targetCount ?? 1 }, (_, index) =>
            h('div', { class: 'feedback-target', 'data-index': index }),
          ),
        )
    },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      const child = reducedMotion.value
        ? h(UiMotionConfig, { reducedMotion: reducedMotion.value }, () =>
            h(Harness),
          )
        : h(Harness)
      return h(UiAppRoot, {}, () => child)
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const elements = [...host.querySelectorAll('.feedback-target')]
  if (
    !elements.every(
      (element): element is HTMLElement => element instanceof HTMLElement,
    )
  )
    throw new Error('Missing feedback target')
  if (!feedback) throw new Error('Missing motion feedback')
  return {
    element: elements[0] as HTMLElement,
    elements,
    feedback,
    reducedMotion,
  }
}

function getKeyframeEffect(animation: Animation) {
  if (!(animation.effect instanceof KeyframeEffect))
    throw new Error('Expected a native KeyframeEffect')
  return animation.effect
}

function getNativeAnimations(element: HTMLElement) {
  const animations = element.getAnimations()
  expect(animations.length).toBeGreaterThan(0)
  expect(
    animations.every((animation) => animation.playState === 'running'),
  ).toBe(true)
  return animations.map((animation) => ({
    animation,
    effect: getKeyframeEffect(animation),
  }))
}

function getPropertyKeyframes(element: HTMLElement, property: string) {
  const animation = getNativeAnimations(element).find(({ effect }) =>
    effect.getKeyframes().some((keyframe) => property in keyframe),
  )
  if (!animation) throw new Error(`Missing ${property} animation`)
  return animation
}

async function finishAnimations(element: HTMLElement) {
  for (const animation of [...element.getAnimations()]) animation.finish()
  await Promise.resolve()
  await Promise.resolve()
}

function nextFrame() {
  return new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
}

function expectRestored(
  element: HTMLElement,
  opacity: string,
  transform: string,
) {
  expect(element.style.opacity).toBe(opacity)
  expect(element.style.transform).toBe(transform)
}

describe('useMotionFeedback', () => {
  it('compiles typed pairs into ordered native effects with deterministic timing', () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'scale(0.72)'

    mounted.feedback.play(
      mounted.element,
      {
        opacity: [0.2, 0.8],
        x: [-12, 4],
        y: [8, -6],
        rotate: [-45, 30],
        scale: [0.9, 0.65],
      },
      0.24,
    )

    const opacity = getPropertyKeyframes(mounted.element, 'opacity')
    expect(opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '0.2', offset: null, computedOffset: 0 },
      { opacity: '0.8', offset: null, computedOffset: 1 },
    ])
    expect(opacity.effect.getTiming()).toMatchObject({
      duration: 240,
      easing: 'cubic-bezier(0.2, 0, 0, 1)',
    })

    const transform = getPropertyKeyframes(mounted.element, 'transform')
    expect(transform.effect.getKeyframes()).toMatchObject([
      {
        transform:
          'translateX(-12px) translateY(8px) rotate(-45deg) scale(0.9)',
        offset: null,
        computedOffset: 0,
      },
      {
        transform: 'translateX(4px) translateY(-6px) rotate(30deg) scale(0.65)',
        offset: null,
        computedOffset: 1,
      },
    ])
    expect(transform.effect.getTiming()).toMatchObject({
      duration: 240,
      easing: 'cubic-bezier(0.2, 0, 0, 1)',
    })
  })

  it('fills every transform key while leaving opacity-only feedback separate', () => {
    const mounted = mountFeedback()

    mounted.feedback.play(mounted.element, { y: [-40, 0] }, 0.14)
    const transform = getPropertyKeyframes(mounted.element, 'transform')
    expect(transform.effect.getKeyframes()).toMatchObject([
      {
        transform: 'translateY(-40px)',
      },
      {
        transform: 'translateY(0px)',
      },
    ])

    mounted.feedback.play(mounted.element, { opacity: [0.2, 0.8] }, 0.14)
    const opacityAnimations = getNativeAnimations(mounted.element).filter(
      ({ effect }) =>
        effect.getKeyframes().some((keyframe) => 'opacity' in keyframe),
    )
    expect(opacityAnimations).toHaveLength(1)
    expect(
      opacityAnimations[0]?.effect
        .getKeyframes()
        .every((keyframe) => !('transform' in keyframe)),
    ).toBe(true)
  })

  it('is running from the first frame and restores only its owned styles on completion', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'translateX(17px) scale(0.72)'

    mounted.feedback.play(
      mounted.element,
      { opacity: [0.2, 0.8], x: [-4, 0] },
      0.24,
    )
    getNativeAnimations(mounted.element)

    await finishAnimations(mounted.element)

    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
  })

  it('cancels and restores synchronously before replacing a play, through the next frame and completion', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'translateX(17px) scale(0.72)'

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const oldAnimations = [...mounted.element.getAnimations()]
    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.24)

    expect(
      oldAnimations.every((animation) => animation.playState === 'idle'),
    ).toBe(true)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
    expect(
      getPropertyKeyframes(
        mounted.element,
        'transform',
      ).effect.getKeyframes()[0]?.transform,
    ).toBe('translateY(-40px)')

    const transformReplacement = [...mounted.element.getAnimations()]
    mounted.feedback.play(mounted.element, { opacity: [0.6, 0.8] }, 0.24)
    expect(
      transformReplacement.every((animation) => animation.playState === 'idle'),
    ).toBe(true)
    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await Promise.resolve()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
    await nextFrame()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await finishAnimations(mounted.element)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    mounted.feedback.play(mounted.element, { rotate: [-45, 45] }, 0.14)
    expect(
      getPropertyKeyframes(mounted.element, 'transform').effect.getKeyframes(),
    ).toMatchObject([
      { transform: 'rotate(-45deg)' },
      { transform: 'rotate(45deg)' },
    ])
    await finishAnimations(mounted.element)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
  })

  it('ignores a stale completion queued before replacement', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const oldAnimations = [...mounted.element.getAnimations()]
    for (const animation of oldAnimations) animation.finish()
    mounted.feedback.play(mounted.element, { opacity: [0.6, 0.8] }, 0.24)

    await Promise.resolve()
    await Promise.resolve()

    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
    expect(mounted.element.getAnimations()[0]?.playState).toBe('running')
    expect(mounted.element.style.opacity).toBe('0.93')
  })

  it('clears empty, undefined, repeated-cancel, resize, preference, and config paths', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    mounted.feedback.play(mounted.element, {})
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', '')

    mounted.feedback.play(undefined, { opacity: [0.1, 0.2] })
    expect(mounted.element.getAnimations()).toHaveLength(0)
    mounted.feedback.cancel(mounted.element)
    mounted.feedback.cancel(mounted.element)

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    window.dispatchEvent(new Event('resize'))
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', '')

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()
    expect(mounted.element.getAnimations()).toHaveLength(0)

    dispose()
    const configured = mountFeedback({ reducedMotion: 'never' })
    configured.feedback.play(configured.element, { opacity: [0.1, 0.2] }, 0.4)
    configured.reducedMotion.value = 'always'
    await nextTick()
    expect(configured.element.getAnimations()).toHaveLength(0)
  })

  it('filters reduced movement, keeps reduced fades native, and restores their baseline', async () => {
    preference.matches = true
    const mounted = mountFeedback()
    mounted.element.style.transform = 'scale(0.72)'

    mounted.feedback.play(mounted.element, { y: [-40, 0] }, 0.4)
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expect(mounted.element.style.transform).toBe('scale(0.72)')

    mounted.feedback.play(
      mounted.element,
      { y: [-40, 0], opacity: [0.2, 0.8] },
      0.4,
    )
    const animations = getNativeAnimations(mounted.element)
    expect(animations).toHaveLength(1)
    expect(animations[0]?.effect.getKeyframes()).toMatchObject([
      { opacity: '0.2' },
      { opacity: '0.8' },
    ])
    expect(
      animations[0]?.effect
        .getKeyframes()
        .every((keyframe) => !('transform' in keyframe)),
    ).toBe(true)

    await finishAnimations(mounted.element)
    expectRestored(mounted.element, '', 'scale(0.72)')
  })

  it('keeps two elements independent and leaves unrelated native animations alive', () => {
    const mounted = mountFeedback({ targetCount: 2 })
    const second = mounted.elements[1]
    if (!second) throw new Error('Missing second target')
    const unrelated = mounted.element.animate(
      { transform: ['translateX(2px)', 'translateX(8px)'] },
      { duration: 1000, fill: 'both' },
    )

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    mounted.feedback.play(second, { y: [-4, 0] }, 0.4)
    mounted.feedback.cancel(mounted.element)
    expect(unrelated.playState).toBe('running')
    expect(mounted.element.getAnimations()).toContain(unrelated)
    expect(second.getAnimations()).not.toHaveLength(0)
    expectRestored(mounted.element, '', '')
  })

  it('disposes active feedback and removes its resize cleanup', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    dispose()

    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', '')
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expectRestored(mounted.element, '0.93', '')
  })
})
