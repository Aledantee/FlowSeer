// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { UiAppRoot, UiMotionConfig } from '../index'
import { useMotionFeedback } from './useMotionFeedback'

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

function mountFeedback(reducedMotion?: 'always' | 'user') {
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

function wait(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

describe('useMotionFeedback', () => {
  it('holds movement during play and restores inline styles after completion', async () => {
    const { element, feedback } = mountFeedback()

    feedback.play(element, { y: [-4, 0], opacity: [0.6, 1] })
    await wait(40)

    expect(element.style.transform).toContain('translateY(')

    await wait(280)
    expect(element.style.transform).toBe('')
    expect(element.style.opacity).toBe('')
  })

  it('keeps the fade while reduced motion filters movement', async () => {
    preference.matches = true
    const { element, feedback } = mountFeedback()

    expect(feedback.reduced.value).toBe(true)
    feedback.play(element, { y: [-4, 0], opacity: [0.6, 1] })
    await wait(40)

    expect(element.style.transform).toBe('')
    expect(element.getAnimations()).not.toHaveLength(0)
  })

  it('treats an always-reduced motion config as reduced', async () => {
    const { element, feedback } = mountFeedback('always')

    expect(feedback.reduced.value).toBe(true)
    feedback.play(element, { y: [-4, 0], opacity: [0.6, 1] })
    await wait(40)

    expect(element.style.transform).toBe('')
    expect(element.getAnimations()).not.toHaveLength(0)
  })

  it('cancels and restores the prior style on resize', () => {
    const { element, feedback } = mountFeedback()
    element.style.opacity = '0.9'

    feedback.play(element, { opacity: [0.5, 0.9] })
    window.dispatchEvent(new Event('resize'))

    expect(element.style.opacity).toBe('0.9')
    expect(element.getAnimations()).toHaveLength(0)
  })

  it('cancels active feedback when the motion preference changes', async () => {
    const { element, feedback } = mountFeedback()
    element.style.transform = 'scale(0.8)'
    feedback.play(element, { y: [-4, 0], opacity: [0.6, 1] })

    preference.matches = true
    preference.dispatchEvent(
      Object.assign(new Event('change'), { matches: true }),
    )
    await nextTick()

    expect(element.style.transform).toBe('scale(0.8)')
    expect(element.getAnimations()).toHaveLength(0)
  })

  it('replaces an animation without allowing the prior completion to clear it', async () => {
    const { element, feedback } = mountFeedback()

    feedback.play(element, { opacity: [0.2, 1] }, 0.2)
    await wait(20)
    feedback.play(element, { opacity: [0.4, 1] }, 0.2)
    await wait(40)

    expect(element.getAnimations()).not.toHaveLength(0)
    await wait(60)
    expect(element.getAnimations()).not.toHaveLength(0)
  })

  it('cancels all feedback when the owning component unmounts', async () => {
    const { element, feedback } = mountFeedback()

    feedback.play(element, { opacity: [0.5, 1] })
    await wait(40)
    expect(element.getAnimations()).not.toHaveLength(0)

    dispose()

    expect(element.getAnimations()).toHaveLength(0)
  })
})
