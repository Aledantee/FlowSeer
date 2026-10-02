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

  it('compiles only supplied transform keys and keeps opacity in a separate effect', () => {
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

  it('leaves a later write to an owned property untouched after every terminal path', async () => {
    const mounted = mountFeedback()
    mounted.element.style.transform = 'scale(0.72)'

    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    mounted.feedback.cancel(mounted.element)
    mounted.element.style.transform = 'translateX(50px)'
    await nextFrame()
    expect(mounted.element.style.transform).toBe('translateX(50px)')

    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    window.dispatchEvent(new Event('resize'))
    mounted.element.style.transform = 'translateX(60px)'
    await nextFrame()
    expect(mounted.element.style.transform).toBe('translateX(60px)')

    mounted.element.style.opacity = '0.93'
    mounted.feedback.play(mounted.element, { opacity: [0.2, 0.8] }, 0.4)
    await finishAnimations(mounted.element)
    mounted.element.style.opacity = '0.5'
    await nextFrame()
    expect(mounted.element.style.opacity).toBe('0.5')
  })

  it('cancels and restores synchronously before replacing a play, through the next frame and completion', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'translateX(17px) scale(0.72)'

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const oldAnimations = [...mounted.element.getAnimations()]
    mounted.element.style.opacity = '0.5'
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

  it('cancels into a play and keeps the original transform through the next frame and completion', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'translateX(17px) scale(0.72)'

    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    mounted.element.style.transform = 'translateY(-99px)'
    mounted.feedback.cancel(mounted.element)
    mounted.feedback.play(mounted.element, { opacity: [0.6, 1] }, 0.24)

    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
    expect(
      getPropertyKeyframes(mounted.element, 'opacity').effect.getKeyframes(),
    ).toMatchObject([{ opacity: '0.6' }, { opacity: '1' }])
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await Promise.resolve()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await nextFrame()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await finishAnimations(mounted.element)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
  })

  it('resizes into a play and keeps the original transform through the next frame and completion', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'translateX(17px) scale(0.72)'

    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    mounted.element.style.transform = 'translateY(-99px)'
    window.dispatchEvent(new Event('resize'))
    mounted.feedback.play(mounted.element, { opacity: [0.6, 1] }, 0.24)

    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
    expect(
      getPropertyKeyframes(mounted.element, 'opacity').effect.getKeyframes(),
    ).toMatchObject([{ opacity: '0.6' }, { opacity: '1' }])
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await Promise.resolve()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await nextFrame()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

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

  it('ends a replacement play through cancel, resize, preference, config, and unmount', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'scale(0.72)'

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const cancelReplaced = [...mounted.element.getAnimations()]
    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    expect(cancelReplaced.every((a) => a.playState === 'idle')).toBe(true)
    mounted.element.style.transform = 'translateX(50px)'
    mounted.feedback.cancel(mounted.element)
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const resizeReplaced = [...mounted.element.getAnimations()]
    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    expect(resizeReplaced.every((a) => a.playState === 'idle')).toBe(true)
    mounted.element.style.transform = 'translateX(50px)'
    window.dispatchEvent(new Event('resize'))
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const preferenceReplaced = [...mounted.element.getAnimations()]
    mounted.feedback.play(mounted.element, { y: [-40, -20] }, 0.4)
    expect(preferenceReplaced.every((a) => a.playState === 'idle')).toBe(true)
    mounted.element.style.transform = 'translateX(50px)'
    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')

    dispose()
    const configured = mountFeedback({ reducedMotion: 'never' })
    configured.element.style.opacity = '0.93'
    configured.element.style.transform = 'scale(0.72)'
    configured.feedback.play(configured.element, { opacity: [0.1, 0.2] }, 0.4)
    const configReplaced = [...configured.element.getAnimations()]
    configured.feedback.play(configured.element, { y: [-40, -20] }, 0.4)
    expect(configReplaced.every((a) => a.playState === 'idle')).toBe(true)
    configured.element.style.transform = 'translateX(50px)'
    configured.reducedMotion.value = 'always'
    await nextTick()
    expect(configured.element.getAnimations()).toHaveLength(0)
    expectRestored(configured.element, '0.93', 'scale(0.72)')

    dispose()
    preference.matches = false
    const unmounted = mountFeedback()
    unmounted.element.style.opacity = '0.93'
    unmounted.element.style.transform = 'scale(0.72)'
    unmounted.feedback.play(unmounted.element, { opacity: [0.1, 0.2] }, 0.4)
    const unmountReplaced = [...unmounted.element.getAnimations()]
    unmounted.feedback.play(unmounted.element, { y: [-40, -20] }, 0.4)
    expect(unmountReplaced.every((a) => a.playState === 'idle')).toBe(true)
    unmounted.element.style.transform = 'translateX(50px)'
    dispose()
    expect(unmounted.element.getAnimations()).toHaveLength(0)
    expectRestored(unmounted.element, '0.93', 'scale(0.72)')
  })

  it('clears empty, undefined, repeated-cancel, resize, preference, and config paths', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'scale(0.72)'

    mounted.feedback.play(
      mounted.element,
      { opacity: [0.1, 0.2], y: [-4, 0] },
      0.4,
    )
    mounted.element.style.opacity = '0.5'
    mounted.element.style.transform = 'translateY(-99px)'
    mounted.feedback.play(mounted.element, {})
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')

    mounted.feedback.play(undefined, { opacity: [0.1, 0.2] })
    expect(mounted.element.getAnimations()).toHaveLength(0)
    mounted.feedback.cancel(mounted.element)
    mounted.feedback.cancel(mounted.element)

    mounted.feedback.play(
      mounted.element,
      { opacity: [0.1, 0.2], y: [-4, 0] },
      0.4,
    )
    mounted.element.style.opacity = '0.5'
    mounted.element.style.transform = 'translateY(-99px)'
    window.dispatchEvent(new Event('resize'))
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')

    mounted.feedback.play(
      mounted.element,
      { opacity: [0.1, 0.2], y: [-4, 0] },
      0.4,
    )
    mounted.element.style.opacity = '0.5'
    mounted.element.style.transform = 'translateY(-99px)'
    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')

    dispose()
    const configured = mountFeedback({ reducedMotion: 'never' })
    configured.element.style.opacity = '0.93'
    configured.element.style.transform = 'scale(0.72)'
    configured.feedback.play(
      configured.element,
      { opacity: [0.1, 0.2], y: [-4, 0] },
      0.4,
    )
    configured.element.style.opacity = '0.5'
    configured.element.style.transform = 'translateY(-99px)'
    configured.reducedMotion.value = 'always'
    await nextTick()
    expect(configured.element.getAnimations()).toHaveLength(0)
    expectRestored(configured.element, '0.93', 'scale(0.72)')
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

  it('preserves a transform changed during an opacity-only play and unrelated native animations', () => {
    const mounted = mountFeedback()
    const unrelated = mounted.element.animate(
      { transform: ['translateX(2px)', 'translateX(8px)'] },
      { duration: 1000, fill: 'both' },
    )

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    mounted.element.style.transform = 'translateX(50px)'
    mounted.feedback.cancel(mounted.element)

    expect(mounted.element.style.transform).toBe('translateX(50px)')
    expect(unrelated.playState).toBe('running')

    mounted.feedback.cancel(mounted.element)
    expect(mounted.element.style.transform).toBe('translateX(50px)')
    expect(unrelated.playState).toBe('running')
  })

  it('removes movement under an always preference without the query and cancels a movement-only play', () => {
    const mounted = mountFeedback({ reducedMotion: 'always' })
    mounted.element.style.opacity = '0.93'
    mounted.element.style.transform = 'scale(0.72)'

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

    mounted.element.style.opacity = '0.5'
    mounted.feedback.play(mounted.element, { y: [-10, 0] }, 0.4)
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'scale(0.72)')
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
    mounted.element.style.opacity = '0.5'
    mounted.feedback.cancel(mounted.element)
    expect(unrelated.playState).toBe('running')
    expect(mounted.element.getAnimations()).toContain(unrelated)
    expect(second.getAnimations()).not.toHaveLength(0)
    expectRestored(mounted.element, '', '')
  })

  it('disposes active feedback and ignores resize after disposal', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    mounted.element.style.opacity = '0.5'
    dispose()

    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', '')

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
  })
})
