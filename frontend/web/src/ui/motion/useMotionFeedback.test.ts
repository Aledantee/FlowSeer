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

const inlineStyleCases: InlineStyleCase[] = [
  { name: 'none', opacity: '', transform: '' },
  { name: 'opacity', opacity: '0.93', transform: '' },
  {
    name: 'opacity and transform',
    opacity: '0.93',
    transform: 'translateX(17px) scale(0.72) rotate(13deg)',
  },
]

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
  replacementStart: number,
  originalOpacity: string,
  originalTransform: string,
  keyframes: MotionKeyframes,
) {
  const earlyFrames = frames.filter(
    (frame) =>
      frame.time >= replacementStart && frame.time < replacementStart + 120,
  )
  expect(earlyFrames.length).toBeGreaterThanOrEqual(2)
  assertTransformInvariant(earlyFrames, originalTransform, keyframes)
  assertOpacityInvariant(earlyFrames, originalOpacity, keyframes)
  const transformNames = Object.keys(transformKeyframes(keyframes))
  if (transformNames.length) {
    expect(
      earlyFrames.some((frame) =>
        transformNames.some((key) =>
          frame.transform.includes(key === 'y' ? 'translateY(' : `${key}(`),
        ),
      ),
    ).toBe(true)
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

  const replacementCases = [
    {
      name: 'same key replacement',
      first: { opacity: [0.1, 0.2] } as MotionKeyframes,
      replacement: { opacity: [0.6, 0.8] } as MotionKeyframes,
    },
    {
      name: 'different key replacement',
      first: {
        rotate: [-45, -30],
        scale: [0.9, 0.72],
        opacity: [0.1, 0.2],
      } as MotionKeyframes,
      replacement: { y: [-40, -20], opacity: [0.6, 0.8] } as MotionKeyframes,
    },
  ]

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
    const replacementStart = performance.now()
    phase = 'replacement'
    mounted.feedback.play(mounted.element, testCase.replacement, 0.24)
    await wait(400)
    recorder.stop()

    assertTransformInvariant(
      recorder.frames.filter((frame) => frame.phase === 'first'),
      testCase.transform,
      testCase.first,
    )
    assertTransformInvariant(
      recorder.frames.filter((frame) => frame.phase === 'replacement'),
      testCase.transform,
      testCase.replacement,
    )
    assertReplacementFrames(
      recorder.frames,
      replacementStart,
      testCase.opacity,
      testCase.transform,
      testCase.replacement,
    )
    assertRestored(recorder.frames, testCase.opacity, testCase.transform)
  })

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
