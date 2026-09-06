// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { animate } from 'motion/mini'
import { useMotionFeedback } from './useMotionFeedback'

const lifecycle = vi.hoisted(() => ({ dispose: () => {} }))
vi.mock('vue', () => ({
  onUnmounted: (callback: () => void) => {
    lifecycle.dispose = callback
  },
}))
vi.mock('motion/mini', () => ({ animate: vi.fn() }))

function animationStub() {
  let complete = () => {}
  return {
    duration: 0.14,
    iterationDuration: 0.14,
    time: 0,
    speed: 1,
    startTime: 0,
    state: 'running' as const,
    finished: Promise.resolve(),
    play: () => {},
    pause: () => {},
    stop: () => {},
    complete: () => {},
    attachTimeline: () => () => {},
    cancel: vi.fn(),
    then: (callback: () => void) => {
      complete = callback
      return Promise.resolve()
    },
    finish: () => complete(),
  }
}
describe('motion feedback lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })
  function setup(reduced = false) {
    const preference = new EventTarget()
    Object.defineProperty(preference, 'matches', {
      value: reduced,
      writable: true,
    })
    vi.spyOn(window, 'matchMedia').mockReturnValue(
      Object.assign(preference, {
        matches: reduced,
        media: '(prefers-reduced-motion: reduce)',
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
      }),
    )
    const element = document.createElement('div')
    const animation = animationStub()
    vi.mocked(animate).mockReturnValue(animation)
    return { element, animation, preference, ...useMotionFeedback() }
  }
  it('skips animation when reduced motion is enabled', () => {
    const { element, play } = setup(true)
    play(element, { opacity: [0.5, 1] })
    expect(animate).not.toHaveBeenCalled()
    lifecycle.dispose()
  })
  it('restores CSS control after Motion writes final inline styles', () => {
    const { element, animation, play } = setup()
    play(element, { marginLeft: ['228px', '72px'] })
    element.style.marginLeft = '72px'
    animation.finish()
    expect(element.style.marginLeft).toBe('')
    expect(animation.cancel).toHaveBeenCalledOnce()
    lifecycle.dispose()
  })
  it('cancels and restores the prior style on resize', () => {
    const { element, animation, play } = setup()
    element.style.opacity = '0.9'
    play(element, { opacity: [0.5, 0.9] })
    element.style.opacity = '0.6'
    window.dispatchEvent(new Event('resize'))
    expect(element.style.opacity).toBe('0.9')
    expect(animation.cancel).toHaveBeenCalledOnce()
    lifecycle.dispose()
  })
  it('cancels active feedback when the motion preference changes', () => {
    const { element, animation, preference, play } = setup()
    play(element, { transform: ['translateX(12px)', 'none'] })
    preference.dispatchEvent(new Event('change'))
    expect(animation.cancel).toHaveBeenCalledOnce()
    lifecycle.dispose()
  })
  it('ignores completion from an animation replaced by a newer interaction', () => {
    const { element, animation, play } = setup()
    play(element, { opacity: [0.5, 1] })
    const next = animationStub()
    vi.mocked(animate).mockReturnValue(next)
    play(element, { opacity: [0.7, 1] })
    animation.finish()
    expect(animation.cancel).toHaveBeenCalledOnce()
    expect(next.cancel).not.toHaveBeenCalled()
    next.finish()
    expect(next.cancel).toHaveBeenCalledOnce()
    lifecycle.dispose()
  })
  it('cancels animation on unmount', () => {
    const { element, animation, play } = setup()
    play(element, { opacity: [0.5, 1] })
    lifecycle.dispose()
    expect(animation.cancel).toHaveBeenCalledOnce()
  })
})
