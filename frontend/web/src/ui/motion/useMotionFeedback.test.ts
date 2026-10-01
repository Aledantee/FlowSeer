// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { UiAppRoot } from '../index'
import { UiMotionConfig } from './index'
import { useMotionFeedback } from './useMotionFeedback'

vi.hoisted(() => {
  vi.stubGlobal(
    'requestAnimationFrame',
    // Motion reads this when its frame loop is imported, and browser frames run
    // after timer tasks scheduled by the same turn.
    (callback: (timestamp: number) => void) =>
      setTimeout(() => callback(performance.now()), 16),
  )
})

type MotionKey = 'opacity' | 'x' | 'y' | 'rotate' | 'scale'
type MotionKeyframes = Partial<Record<MotionKey, [number, number]>>
type TransformKey = Exclude<MotionKey, 'opacity'>
type TransformValues = Partial<Record<TransformKey, number>>

interface FrameSample {
  phase: string
  time: number
  opacity: string
  transform: string
}

interface PlayCase {
  name: string
  keyframes: MotionKeyframes
}

interface InlineStyleCase {
  name: string
  opacity: string
  transform: string
}

interface EndPath {
  name: string
  run: (
    args: {
      element: HTMLElement
      feedback: ReturnType<typeof useMotionFeedback>
      setPhase: (phase: string) => void
      dispose: () => void
    },
    keyframes: MotionKeyframes,
  ) => Promise<void>
}

interface ReplacementEndPath {
  name: string
  run: (
    args: {
      element: HTMLElement
      feedback: ReturnType<typeof useMotionFeedback>
      setPhase: (phase: string) => void
      dispose: () => void
    },
    first: MotionKeyframes,
    replacement: MotionKeyframes,
  ) => Promise<void>
}

interface ReplacementCase {
  name: string
  first: MotionKeyframes
  replacement: MotionKeyframes
}

const playCases: PlayCase[] = [
  { name: 'opacity', keyframes: { opacity: [0.2, 0.8] } },
  { name: 'y', keyframes: { y: [-40, 0] } },
  {
    name: 'y and opacity',
    keyframes: { y: [-40, 0], opacity: [0.2, 0.8] },
  },
  {
    name: 'rotate, scale, and opacity',
    keyframes: {
      rotate: [-45, 45],
      scale: [0.9, 0.65],
      opacity: [0.2, 0.8],
    },
  },
]

const replacementCases: ReplacementCase[] = [
  {
    name: 'same key replacement',
    first: { opacity: [0.1, 0.2] },
    replacement: { opacity: [0.6, 0.8] },
  },
  {
    name: 'different transform key replacement',
    first: {
      rotate: [-45, -30],
      scale: [0.9, 0.72],
      opacity: [0.1, 0.2],
    },
    replacement: { y: [-40, -20], opacity: [0.6, 0.8] },
  },
  {
    name: 'same y and opacity replacement',
    first: { y: [-40, 0], opacity: [0.2, 1] },
    replacement: { y: [-30, 0], opacity: [0.5, 1] },
  },
  {
    name: 'same rotate, scale, and opacity replacement',
    first: {
      rotate: [-45, 45],
      scale: [0.9, 0.65],
      opacity: [0.2, 1],
    },
    replacement: {
      rotate: [-30, 30],
      scale: [0.8, 0.7],
      opacity: [0.5, 1],
    },
  },
  {
    name: 'opacity to transform replacement',
    first: { opacity: [0.1, 0.2] },
    replacement: { y: [-40, -20] },
  },
  {
    name: 'transform to opacity replacement',
    first: { y: [-40, -20] },
    replacement: { opacity: [0.6, 0.8] },
  },
  {
    name: 'same transform key replacement',
    first: { y: [-40, -20] },
    replacement: { y: [20, 40] },
  },
]

const replacementEndPaths: ReplacementEndPath[] = [
  {
    name: 'cancel from a timer task',
    async run({ element, feedback, setPhase }, first, replacement) {
      setPhase('first')
      feedback.play(element, first, 0.4)
      await wait(48)
      setPhase('replacement')
      feedback.play(element, replacement, 0.24)
      window.setTimeout(() => feedback.cancel(element), 0)
      await wait(32)
      setPhase('ended')
      await wait(148)
    },
  },
  {
    name: 'window resize',
    async run({ element, feedback, setPhase }, first, replacement) {
      setPhase('first')
      feedback.play(element, first, 0.4)
      await wait(48)
      setPhase('replacement')
      feedback.play(element, replacement, 0.24)
      window.dispatchEvent(new Event('resize'))
      setPhase('ended')
      await wait(180)
    },
  },
  {
    name: 'motion preference change',
    async run({ element, feedback, setPhase }, first, replacement) {
      setPhase('first')
      feedback.play(element, first, 0.4)
      await wait(48)
      setPhase('replacement')
      feedback.play(element, replacement, 0.24)
      preference.matches = true
      preference.dispatchEvent(
        Object.assign(new Event('change'), { matches: true }),
      )
      await nextTick()
      setPhase('ended')
      await wait(180)
    },
  },
  {
    name: 'unmount',
    async run({ element, feedback, setPhase, dispose }, first, replacement) {
      setPhase('first')
      feedback.play(element, first, 0.4)
      await wait(48)
      setPhase('replacement')
      feedback.play(element, replacement, 0.24)
      dispose()
      setPhase('ended')
      await wait(180)
    },
  },
]

const inlineStyleCases: InlineStyleCase[] = [
  { name: 'none', opacity: '', transform: '' },
  { name: 'opacity', opacity: '0.93', transform: '' },
  {
    name: 'opacity and transform',
    opacity: '0.93',
    transform: 'translateX(17px) scale(0.72) rotate(13deg)',
  },
]

const generatedReplacementEndCases = replacementEndPaths.flatMap((path) =>
  replacementCases.flatMap((replacementCase) =>
    inlineStyleCases.map((inlineStyles) => ({
      ...path,
      ...replacementCase,
      ...inlineStyles,
      caseName: `${path.name} / ${replacementCase.name} / ${inlineStyles.name}`,
    })),
  ),
)

const endPaths: EndPath[] = [
  {
    name: 'completion',
    async run({ element, feedback, setPhase }, keyframes) {
      setPhase('first')
      feedback.play(element, keyframes, 0.08)
      await wait(180)
    },
  },
  {
    name: 'cancel from a timer task',
    async run({ element, feedback, setPhase }, keyframes) {
      setPhase('first')
      feedback.play(element, keyframes, 0.4)
      await wait(32)
      window.setTimeout(() => feedback.cancel(element), 0)
      await wait(180)
    },
  },
  {
    name: 'window resize',
    async run({ element, feedback, setPhase }, keyframes) {
      setPhase('first')
      feedback.play(element, keyframes, 0.4)
      await wait(32)
      window.dispatchEvent(new Event('resize'))
      await wait(180)
    },
  },
  {
    name: 'motion preference change',
    async run({ element, feedback, setPhase }, keyframes) {
      setPhase('first')
      feedback.play(element, keyframes, 0.4)
      await wait(32)
      preference.matches = true
      preference.dispatchEvent(
        Object.assign(new Event('change'), { matches: true }),
      )
      await nextTick()
      await wait(180)
    },
  },
  {
    name: 'unmount',
    async run({ element, feedback, setPhase, dispose }, keyframes) {
      setPhase('first')
      feedback.play(element, keyframes, 0.4)
      await wait(32)
      dispose()
      await wait(180)
    },
  },
]

const generatedEndCases = endPaths.flatMap((path) =>
  playCases.flatMap((playCase) =>
    inlineStyleCases.map((inlineStyles) => ({
      ...path,
      ...playCase,
      ...inlineStyles,
      caseName: `${path.name} / ${playCase.name} / ${inlineStyles.name}`,
    })),
  ),
)

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

function mountFeedback(reducedMotion?: 'always') {
  let feedback: ReturnType<typeof useMotionFeedback> | undefined
  const Harness = defineComponent({
    setup() {
      feedback = useMotionFeedback()
      return () => h('div', { class: 'feedback-target' })
    },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      const child = reducedMotion
        ? h(UiMotionConfig, { reducedMotion }, () => h(Harness))
        : h(Harness)
      return h(UiAppRoot, {}, () => child)
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const element = host.querySelector('.feedback-target')
  if (!(element instanceof HTMLElement))
    throw new Error('Missing feedback target')
  if (!feedback) throw new Error('Missing motion feedback')
  return { element, feedback }
}

function recordFrames(element: HTMLElement, getPhase: () => string) {
  const frames: FrameSample[] = []
  let recording = true

  const record = () => {
    if (!recording) return
    frames.push({
      phase: getPhase(),
      time: performance.now(),
      opacity: element.style.opacity,
      transform: element.style.transform,
    })
    requestAnimationFrame(record)
  }

  requestAnimationFrame(record)
  return {
    frames,
    stop() {
      recording = false
    },
  }
}

function parseTransform(transform: string): TransformValues {
  const values: TransformValues = {}
  const names: Record<string, TransformKey> = {
    translateX: 'x',
    translateY: 'y',
    rotate: 'rotate',
    scale: 'scale',
  }
  for (const match of transform.matchAll(/([a-z]+)\((-?[\d.]+)/gi)) {
    const key = names[match[1] ?? '']
    if (key) values[key] = Number(match[2])
  }
  return values
}

function transformKeyframes(keyframes: MotionKeyframes) {
  return Object.fromEntries(
    Object.entries(keyframes).filter(([key]) => key !== 'opacity'),
  ) as Partial<Record<TransformKey, [number, number]>>
}

function isWithin(value: number, pair: [number, number]) {
  const low = Math.min(...pair) - 0.01
  const high = Math.max(...pair) + 0.01
  return value >= low && value <= high
}

function expectWithin(value: number, pair: [number, number]) {
  const low = Math.min(...pair) - 0.01
  const high = Math.max(...pair) + 0.01
  expect(value).toBeGreaterThanOrEqual(low)
  expect(value).toBeLessThanOrEqual(high)
}

function assertTransformInvariant(
  frames: FrameSample[],
  originalTransform: string,
  keyframes: MotionKeyframes,
) {
  const activeKeyframes = transformKeyframes(keyframes)
  for (const frame of frames) {
    if (frame.transform === originalTransform) continue
    const values = parseTransform(frame.transform)
    for (const [key, value] of Object.entries(values) as [
      TransformKey,
      number,
    ][]) {
      const pair = activeKeyframes[key]
      expect(
        pair,
        `${key} must belong to the active play in ${frame.phase}: ${frame.transform}`,
      ).toBeDefined()
      if (pair === undefined) throw new Error(`Missing keyframes for ${key}`)
      expectWithin(value, pair)
    }
  }
}

function assertOpacityInvariant(
  frames: FrameSample[],
  originalOpacity: string,
  keyframes: MotionKeyframes,
) {
  const pair = keyframes.opacity
  if (!pair) return
  for (const frame of frames) {
    if (frame.opacity === originalOpacity) continue
    expectWithin(Number(frame.opacity || 0), pair)
  }
}

function isActiveTransformFrame(
  frame: FrameSample,
  keyframes: Partial<Record<TransformKey, [number, number]>>,
) {
  const values = parseTransform(frame.transform)
  const hasActiveValue = Object.entries(values).some(([key, value]) => {
    const pair = keyframes[key as TransformKey]
    return pair !== undefined && isWithin(value, pair)
  })
  return (
    hasActiveValue &&
    Object.entries(values).every(([key, value]) => {
      const pair = keyframes[key as TransformKey]
      return pair !== undefined && isWithin(value, pair)
    })
  )
}

function assertPlayInvariant(
  frames: FrameSample[],
  originalOpacity: string,
  originalTransform: string,
  keyframes: MotionKeyframes,
) {
  const activeTransformKeyframes = transformKeyframes(keyframes)
  const transformNames = Object.keys(activeTransformKeyframes) as TransformKey[]
  if (transformNames.length) {
    const firstActiveFrame = frames.findIndex((frame) =>
      isActiveTransformFrame(frame, activeTransformKeyframes),
    )
    expect(firstActiveFrame).toBeGreaterThanOrEqual(0)
    assertTransformInvariant(
      frames.slice(firstActiveFrame),
      originalTransform,
      keyframes,
    )
  }

  const opacityPair = keyframes.opacity
  if (opacityPair) {
    const firstActiveFrame = frames.findIndex((frame) =>
      isWithin(Number(frame.opacity || 0), opacityPair),
    )
    expect(firstActiveFrame).toBeGreaterThanOrEqual(0)
    assertOpacityInvariant(
      frames.slice(firstActiveFrame),
      originalOpacity,
      keyframes,
    )
  }
}

function assertRestored(
  frames: FrameSample[],
  originalOpacity: string,
  originalTransform: string,
) {
  expect(frames.length).toBeGreaterThanOrEqual(4)
  const tail = frames.slice(-4)
  for (const frame of tail) {
    expect(frame.opacity).toBe(originalOpacity)
    expect(frame.transform).toBe(originalTransform)
  }
}

function assertReplacementFrames(
  frames: FrameSample[],
  originalOpacity: string,
  originalTransform: string,
  keyframes: MotionKeyframes,
) {
  const replacementFrames = frames.filter(
    (frame) => frame.phase === 'replacement',
  )
  if (replacementFrames.length < 2) return

  const activeTransformKeyframes = transformKeyframes(keyframes)
  const transformNames = Object.keys(activeTransformKeyframes) as TransformKey[]
  if (transformNames.length) {
    const firstActiveFrame = replacementFrames.findIndex((frame) =>
      isActiveTransformFrame(frame, activeTransformKeyframes),
    )
    expect(firstActiveFrame).toBeGreaterThanOrEqual(0)
    const activeFrames = replacementFrames.slice(firstActiveFrame)
    const restoredFrame = activeFrames.findIndex(
      (frame, index) => index > 0 && frame.transform === originalTransform,
    )
    const framesUntilRestore =
      restoredFrame === -1 ? activeFrames : activeFrames.slice(0, restoredFrame)
    for (const frame of framesUntilRestore) {
      expect(frame.transform).not.toBe(originalTransform)
      const values = parseTransform(frame.transform)
      for (const [key, value] of Object.entries(values) as [
        TransformKey,
        number,
      ][]) {
        const pair = activeTransformKeyframes[key]
        expect(
          pair,
          `${key} must belong to the active replacement: ${frame.transform}`,
        ).toBeDefined()
        if (pair === undefined) throw new Error(`Missing keyframes for ${key}`)
        expectWithin(value, pair)
      }
    }
  }

  const opacityPair = keyframes.opacity
  if (opacityPair) {
    const firstActiveFrame = replacementFrames.findIndex((frame) =>
      isWithin(Number(frame.opacity || 0), opacityPair),
    )
    expect(firstActiveFrame).toBeGreaterThanOrEqual(0)
    const activeFrames = replacementFrames.slice(firstActiveFrame)
    const restoredFrame = activeFrames.findIndex(
      (frame, index) => index > 0 && frame.opacity === originalOpacity,
    )
    const framesUntilRestore =
      restoredFrame === -1 ? activeFrames : activeFrames.slice(0, restoredFrame)
    for (const frame of framesUntilRestore) {
      expect(frame.opacity).not.toBe(originalOpacity)
      expectWithin(Number(frame.opacity || 0), opacityPair)
    }
  }
}

function wait(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

describe('useMotionFeedback invariants', () => {
  it.each(generatedEndCases)('$caseName restores styles', async (testCase) => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = testCase.opacity
    mounted.element.style.transform = testCase.transform
    let phase = 'before'
    const recorder = recordFrames(mounted.element, () => phase)

    await testCase.run(
      {
        element: mounted.element,
        feedback: mounted.feedback,
        setPhase(nextPhase) {
          phase = nextPhase
        },
        dispose,
      },
      testCase.keyframes,
    )
    recorder.stop()

    assertTransformInvariant(
      recorder.frames,
      testCase.transform,
      testCase.keyframes,
    )
    assertOpacityInvariant(
      recorder.frames,
      testCase.opacity,
      testCase.keyframes,
    )
    assertRestored(recorder.frames, testCase.opacity, testCase.transform)
  })

  it.each(inlineStyleCases)(
    'reduced play without opacity restores $name styles',
    async (inlineStyles) => {
      const mounted = mountFeedback('always')
      mounted.element.style.opacity = inlineStyles.opacity
      mounted.element.style.transform = inlineStyles.transform
      let phase = 'before'
      const recorder = recordFrames(mounted.element, () => phase)
      phase = 'reduced play'
      mounted.feedback.play(mounted.element, { y: [-40, 0] }, 0.4)
      await wait(180)
      recorder.stop()

      expect(mounted.feedback.reduced.value).toBe(true)
      assertTransformInvariant(recorder.frames, inlineStyles.transform, {
        y: [-40, 0],
      })
      assertRestored(
        recorder.frames,
        inlineStyles.opacity,
        inlineStyles.transform,
      )
    },
  )

  it.each(
    replacementCases.flatMap((replacementCase) =>
      inlineStyleCases.map((inlineStyles) => ({
        ...replacementCase,
        ...inlineStyles,
        caseName: `${replacementCase.name} / ${inlineStyles.name}`,
      })),
    ),
  )('$caseName has no reset frame', async (testCase) => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = testCase.opacity
    mounted.element.style.transform = testCase.transform
    let phase = 'before'
    const recorder = recordFrames(mounted.element, () => phase)

    phase = 'first'
    mounted.feedback.play(mounted.element, testCase.first, 0.4)
    await wait(48)
    phase = 'replacement'
    mounted.feedback.play(mounted.element, testCase.replacement, 0.24)
    await wait(400)
    phase = 'ended'
    await wait(200)
    recorder.stop()

    assertTransformInvariant(
      recorder.frames.filter((frame) => frame.phase === 'first'),
      testCase.transform,
      testCase.first,
    )
    assertReplacementFrames(
      recorder.frames,
      testCase.opacity,
      testCase.transform,
      testCase.replacement,
    )
    assertRestored(recorder.frames, testCase.opacity, testCase.transform)
  })

  it.each(generatedReplacementEndCases)(
    '$caseName restores a replacement ended in the same turn',
    async (testCase) => {
      const mounted = mountFeedback()
      mounted.element.style.opacity = testCase.opacity
      mounted.element.style.transform = testCase.transform
      let phase = 'before'
      const recorder = recordFrames(mounted.element, () => phase)

      await testCase.run(
        {
          element: mounted.element,
          feedback: mounted.feedback,
          setPhase(nextPhase) {
            phase = nextPhase
          },
          dispose,
        },
        testCase.first,
        testCase.replacement,
      )
      recorder.stop()

      assertTransformInvariant(
        recorder.frames.filter((frame) => frame.phase === 'first'),
        testCase.transform,
        testCase.first,
      )
      assertRestored(recorder.frames, testCase.opacity, testCase.transform)
    },
  )

  it('keeps a normal play between its keyframes before restoring inline styles', async () => {
    const mounted = mountFeedback()
    let phase = 'before'
    const recorder = recordFrames(mounted.element, () => phase)

    phase = 'play'
    mounted.feedback.play(
      mounted.element,
      { y: [-4, 0], opacity: [0.6, 1] },
      0.24,
    )
    await wait(48)

    const activeFrames = recorder.frames.filter(
      (frame) => frame.phase === 'play',
    )
    expect(
      activeFrames.some((frame) => {
        const value = parseTransform(frame.transform).y
        return value !== undefined && value > -3.99 && value < -0.01
      }),
    ).toBe(true)

    await wait(280)
    phase = 'ended'
    await wait(80)
    recorder.stop()

    assertTransformInvariant(recorder.frames, '', {
      y: [-4, 0],
    })
    assertOpacityInvariant(recorder.frames, '', { opacity: [0.6, 1] })
    assertRestored(recorder.frames, '', '')
  })

  it('does not write the pre-chain opacity during replacement', async () => {
    const mounted = mountFeedback()
    mounted.element.style.opacity = '0.93'
    let phase = 'before'
    const writes: Array<{ phase: string; value: string }> = []
    const style = mounted.element.style
    const observedStyle = new Proxy(style, {
      set(target, property, value, receiver) {
        if (property === 'opacity') {
          writes.push({ phase, value: String(value) })
        }
        return Reflect.set(target, property, value, receiver)
      },
    })
    Object.defineProperty(mounted.element, 'style', {
      configurable: true,
      value: observedStyle,
    })

    mounted.feedback.play(mounted.element, { opacity: [0.1, 0.2] }, 0.4)
    await wait(48)
    phase = 'replacement'
    mounted.feedback.play(mounted.element, { opacity: [0.6, 0.8] }, 0.24)
    await wait(80)

    expect(
      writes.some(
        (write) => write.phase === 'replacement' && write.value === '0.93',
      ),
    ).toBe(false)
    Object.defineProperty(mounted.element, 'style', {
      configurable: true,
      value: style,
    })
  })

  it.each([
    { name: 'user preference', reducedMotion: undefined, matches: true },
    { name: 'always', reducedMotion: 'always' as const, matches: false },
  ])(
    '$name reduced motion keeps movement at its original value while fading',
    async (testCase) => {
      preference.matches = testCase.matches
      const mounted = mountFeedback(testCase.reducedMotion)
      const originalOpacity = mounted.element.style.opacity
      const originalTransform = mounted.element.style.transform
      let phase = 'before'
      const recorder = recordFrames(mounted.element, () => phase)

      phase = 'reduced'
      mounted.feedback.play(
        mounted.element,
        { y: [-4, 0], opacity: [0.6, 1] },
        0.4,
      )
      await wait(48)

      expect(mounted.feedback.reduced.value).toBe(true)
      expect(mounted.element.getAnimations()).toHaveLength(1)
      expect(mounted.element.getAnimations()[0]?.playState).toBe('running')
      expect(
        recorder.frames
          .filter((frame) => frame.phase === 'reduced')
          .every((frame) => frame.transform === originalTransform),
      ).toBe(true)

      await wait(180)
      phase = 'ended'
      await wait(80)
      recorder.stop()
      assertRestored(recorder.frames, originalOpacity, originalTransform)
    },
  )

  it.each([
    {
      name: 'three transform plays',
      plays: [
        { y: [-40, -20] },
        { rotate: [-45, -30] },
        { scale: [0.9, 0.72] },
      ] as MotionKeyframes[],
    },
  ])('$name keeps each play isolated', async (testCase) => {
    const mounted = mountFeedback()
    const originalOpacity = mounted.element.style.opacity
    const originalTransform = mounted.element.style.transform
    let phase = 'before'
    const recorder = recordFrames(mounted.element, () => phase)

    for (const [index, keyframes] of testCase.plays.entries()) {
      phase = `play ${index}`
      mounted.feedback.play(mounted.element, keyframes, 0.24)
      await wait(64)
    }
    await wait(280)
    phase = 'ended'
    await wait(80)
    recorder.stop()

    for (const [index, keyframes] of testCase.plays.entries()) {
      assertPlayInvariant(
        recorder.frames.filter((frame) => frame.phase === `play ${index}`),
        originalOpacity,
        originalTransform,
        keyframes,
      )
    }
    assertRestored(recorder.frames, originalOpacity, originalTransform)
  })

  it.each(['user preference', 'always'] as const)(
    'reduced motion replacement keeps movement out of the chain: %s',
    async (mode) => {
      preference.matches = mode === 'user preference'
      const mounted = mountFeedback(mode === 'always' ? 'always' : undefined)
      let phase = 'before'
      const recorder = recordFrames(mounted.element, () => phase)

      phase = 'first'
      mounted.feedback.play(
        mounted.element,
        { y: [-40, -20], opacity: [0.1, 0.2] },
        0.4,
      )
      await wait(48)
      phase = 'replacement'
      mounted.feedback.play(
        mounted.element,
        { rotate: [-45, -30], opacity: [0.6, 0.8] },
        0.24,
      )
      await wait(48)

      expect(mounted.feedback.reduced.value).toBe(true)
      expect(
        recorder.frames
          .filter((frame) => frame.phase === 'replacement')
          .every((frame) => frame.transform === ''),
      ).toBe(true)
      expect(mounted.element.getAnimations()).toHaveLength(1)
      expect(mounted.element.getAnimations()[0]?.playState).toBe('running')

      await wait(180)
      phase = 'ended'
      await wait(80)
      recorder.stop()
      assertRestored(recorder.frames, '', '')
    },
  )

  it.each(inlineStyleCases)(
    'sequential plays with different transform keys keep $name isolated',
    async (inlineStyles) => {
      const mounted = mountFeedback()
      mounted.element.style.opacity = inlineStyles.opacity
      mounted.element.style.transform = inlineStyles.transform
      let phase = 'before'
      const recorder = recordFrames(mounted.element, () => phase)

      phase = 'first'
      mounted.feedback.play(
        mounted.element,
        { rotate: [-45, 45], scale: [0.9, 0.65], opacity: [0.2, 0.8] },
        0.08,
      )
      await wait(180)
      phase = 'second'
      mounted.feedback.play(
        mounted.element,
        { y: [-40, -20], opacity: [0.6, 0.8] },
        0.24,
      )
      await wait(400)
      recorder.stop()

      assertTransformInvariant(
        recorder.frames.filter((frame) => frame.phase === 'first'),
        inlineStyles.transform,
        { rotate: [-45, 45], scale: [0.9, 0.65] },
      )
      assertTransformInvariant(
        recorder.frames.filter((frame) => frame.phase === 'second'),
        inlineStyles.transform,
        { y: [-40, -20] },
      )
      assertRestored(
        recorder.frames,
        inlineStyles.opacity,
        inlineStyles.transform,
      )
    },
  )
})
