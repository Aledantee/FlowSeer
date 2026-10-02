// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { frame } from 'motion-v'
import { createApp, defineComponent, h, nextTick, ref, type Ref } from 'vue'
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

type Feedback = ReturnType<typeof useMotionFeedback>
type OwnedKey = 'opacity' | 'transform'
type InvariantId = 'restore' | 'later-write' | 'snapshot'

interface PlayKeyframes {
  opacity?: [number, number]
  x?: [number, number]
  y?: [number, number]
  rotate?: [number, number]
  scale?: [number, number]
}

interface MountOptions {
  reducedMotion?: 'always' | 'never'
  targetCount?: number
}

interface MountedFeedback {
  element: HTMLElement
  elements: HTMLElement[]
  feedback: Feedback
  reducedMotion: Ref<'always' | 'never' | undefined>
  dispose: () => void
}

interface Baseline {
  readonly name: string
  readonly opacity: string
  readonly transform: string
}

interface OwnedSet {
  readonly name: string
  readonly keys: readonly OwnedKey[]
  readonly keyframes: PlayKeyframes
}

interface TerminalPath {
  readonly name: string
  readonly mountOptions?: MountOptions
  readonly terminate: (
    mounted: MountedFeedback,
    owned: OwnedSet,
  ) => void | Promise<void>
  readonly dirty?: (mounted: MountedFeedback, owned: OwnedSet) => void
  readonly exempt?: (
    invariant: InvariantId,
    owned: OwnedSet,
  ) => string | undefined
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
  let feedback: Feedback | undefined
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
  const unmount = () => {
    app.unmount()
  }
  dispose = unmount
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
    dispose: unmount,
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

// motion-dom captures requestAnimationFrame when its frame loop module loads,
// so the hoisted stub above is the only clock its batches follow. A wait on the
// global frame can resolve before that batch, which is why the frame-batch
// invariant waits on motion's own postRender step and then one global frame.
function motionFrameBatch() {
  return new Promise<void>((resolve) => frame.postRender(() => resolve()))
}

async function settleFrames() {
  await motionFrameBatch()
  await nextFrame()
}

function expectRestored(
  element: HTMLElement,
  opacity: string,
  transform: string,
) {
  expect(element.style.opacity).toBe(opacity)
  expect(element.style.transform).toBe(transform)
}

const baselines: readonly Baseline[] = [
  { name: 'empty inline values', opacity: '', transform: '' },
  {
    name: 'a populated inline baseline',
    opacity: '0.93',
    transform: 'translateX(17px) scale(0.72) rotate(13deg)',
  },
]

const ownedSets: readonly OwnedSet[] = [
  { name: 'opacity', keys: ['opacity'], keyframes: { opacity: [0.2, 0.8] } },
  { name: 'transform', keys: ['transform'], keyframes: { y: [-40, -20] } },
  {
    name: 'both',
    keys: ['opacity', 'transform'],
    keyframes: { opacity: [0.2, 0.8], y: [-40, -20] },
  },
]

const nextKeyframes: Record<string, PlayKeyframes> = {
  opacity: { opacity: [0.6, 1] },
  transform: { y: [-4, 0] },
  both: { opacity: [0.6, 1], y: [-4, 0] },
}

const writtenOffBaseline = { opacity: '0.11', transform: 'translateY(-99px)' }
const writtenAfterPath = { opacity: '0.17', transform: 'translateX(123px)' }

const allKeys: readonly OwnedKey[] = ['opacity', 'transform']

function unownedKeys(owned: OwnedSet) {
  return allKeys.filter((key) => !owned.keys.includes(key))
}

function expectOwnedValues(
  element: HTMLElement,
  owned: OwnedSet,
  baseline: Baseline,
) {
  for (const key of owned.keys) expect(element.style[key]).toBe(baseline[key])
}

function expectUnownedValuesKept(element: HTMLElement, owned: OwnedSet) {
  for (const key of unownedKeys(owned))
    expect(element.style[key]).toBe(writtenOffBaseline[key])
}

const reducedFollowUp = (
  invariant: InvariantId,
  owned: OwnedSet,
): string | undefined =>
  invariant === 'snapshot' && owned.name !== 'opacity'
    ? 'the reduced mode the path selects drops movement from the next play'
    : undefined

const terminalPaths: readonly TerminalPath[] = [
  {
    name: 'a cancel',
    terminate: (mounted) => {
      mounted.feedback.cancel(mounted.element)
    },
  },
  {
    name: 'a window resize',
    terminate: () => {
      window.dispatchEvent(new Event('resize'))
    },
  },
  {
    name: 'a reduced-motion preference change',
    terminate: async () => {
      preference.matches = true
      preference.dispatchEvent(
        Object.assign(new Event('change'), { matches: true }),
      )
      await nextTick()
    },
    exempt: reducedFollowUp,
  },
  {
    name: 'a config change',
    mountOptions: { reducedMotion: 'never' },
    terminate: async (mounted) => {
      mounted.reducedMotion.value = 'always'
      await nextTick()
    },
    exempt: reducedFollowUp,
  },
  {
    name: 'an unmount',
    terminate: (mounted) => {
      mounted.dispose()
    },
    exempt: (invariant) =>
      invariant === 'snapshot'
        ? 'the scope is disposed, so no later play shares it'
        : undefined,
  },
  {
    name: 'a native completion',
    terminate: async (mounted) => {
      await finishAnimations(mounted.element)
    },
  },
  {
    name: 'a replacement play',
    // A native finish writes the play's final value inline and queues its
    // completion, so the replacement runs against the value the play left.
    dirty: (mounted) => {
      for (const animation of [...mounted.element.getAnimations()])
        animation.finish()
    },
    terminate: (mounted, owned) => {
      mounted.feedback.play(mounted.element, nextKeyframes[owned.name], 0.24)
    },
  },
  {
    name: 'an empty play',
    terminate: (mounted) => {
      mounted.feedback.play(mounted.element, {})
    },
  },
  {
    name: 'a movement-only play under reduced motion',
    mountOptions: { reducedMotion: 'always' },
    terminate: (mounted) => {
      mounted.feedback.play(mounted.element, { y: [-10, 0] })
    },
    exempt: (_invariant, owned) =>
      owned.name === 'opacity'
        ? undefined
        : 'reduced motion drops movement before the play can own it',
  },
]

const invariants = [
  {
    id: 'restore',
    title: 'restores the owned inline values in the same turn',
  },
  {
    id: 'later-write',
    title: 'keeps a later write to an owned property through the frame batch',
  },
  {
    id: 'snapshot',
    title: 'lets the play started in the same turn snapshot the baseline',
  },
] as const satisfies readonly { id: InvariantId; title: string }[]

function startPlay(
  mounted: MountedFeedback,
  owned: OwnedSet,
  baseline: Baseline,
) {
  mounted.element.style.opacity = baseline.opacity
  mounted.element.style.transform = baseline.transform
  mounted.feedback.play(mounted.element, owned.keyframes, 0.4)
  const created = [...mounted.element.getAnimations()]
  expect(created.length).toBeGreaterThan(0)
  expect(created.every((animation) => animation.playState === 'running')).toBe(
    true,
  )
  for (const key of unownedKeys(owned))
    mounted.element.style[key] = writtenOffBaseline[key]
  return created
}

function writeOwnedOffBaseline(
  mounted: MountedFeedback,
  owned: OwnedSet,
  path: TerminalPath,
) {
  if (path.dirty) {
    path.dirty(mounted, owned)
    return
  }
  for (const key of owned.keys)
    mounted.element.style[key] = writtenOffBaseline[key]
}

function registerTerminalCases(
  path: TerminalPath,
  owned: OwnedSet,
  baseline: Baseline,
) {
  for (const invariant of invariants) {
    const exemption = path.exempt?.(invariant.id, owned)
    const register = exemption === undefined ? it : it.skip
    const title =
      exemption === undefined
        ? invariant.title
        : `${invariant.title} (exempt: ${exemption})`
    register(title, async () => {
      const mounted = mountFeedback(path.mountOptions)
      const created = startPlay(mounted, owned, baseline)
      writeOwnedOffBaseline(mounted, owned, path)
      await path.terminate(mounted, owned)

      if (invariant.id === 'restore') {
        expectOwnedValues(mounted.element, owned, baseline)
        expectUnownedValuesKept(mounted.element, owned)
        expect(
          created.every((animation) => animation.playState !== 'running'),
        ).toBe(true)
        return
      }

      if (invariant.id === 'later-write') {
        for (const key of owned.keys)
          mounted.element.style[key] = writtenAfterPath[key]
        await settleFrames()
        for (const key of owned.keys)
          expect(mounted.element.style[key]).toBe(writtenAfterPath[key])
        return
      }

      const previous = [...mounted.element.getAnimations()]
      mounted.feedback.play(
        mounted.element,
        nextKeyframes[owned.name] as PlayKeyframes,
        0.24,
      )
      expectOwnedValues(mounted.element, owned, baseline)
      const createdNext = mounted.element
        .getAnimations()
        .filter((animation) => !previous.includes(animation))
      expect(createdNext.length).toBeGreaterThan(0)
      expect(
        createdNext.every((animation) => animation.playState === 'running'),
      ).toBe(true)
      await finishAnimations(mounted.element)
      expectOwnedValues(mounted.element, owned, baseline)
    })
  }
}

describe('useMotionFeedback across its terminal paths', () => {
  describe.each(terminalPaths)('$name', (path) => {
    describe.each(ownedSets)('owning $name', (owned) => {
      describe.each(baselines)('from $name', (baseline) => {
        registerTerminalCases(path, owned, baseline)
      })
    })
  })
})

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
    await settleFrames()
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
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
    mounted.feedback.play(mounted.element, { opacity: [0.6, 1] }, 0.24)

    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
    expect(
      getPropertyKeyframes(mounted.element, 'opacity').effect.getKeyframes(),
    ).toMatchObject([{ opacity: '0.6' }, { opacity: '1' }])
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await Promise.resolve()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await settleFrames()
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
    expect(mounted.element.getAnimations()).toHaveLength(0)
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')
    mounted.feedback.play(mounted.element, { opacity: [0.6, 1] }, 0.24)

    expect(getNativeAnimations(mounted.element)).toHaveLength(1)
    expect(
      getPropertyKeyframes(mounted.element, 'opacity').effect.getKeyframes(),
    ).toMatchObject([{ opacity: '0.6' }, { opacity: '1' }])
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await Promise.resolve()
    expectRestored(mounted.element, '0.93', 'translateX(17px) scale(0.72)')

    await settleFrames()
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

    await finishAnimations(mounted.element)
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
    mounted.feedback.cancel(mounted.element)
  })
})
