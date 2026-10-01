// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { UiAppRoot } from '../ui'
import ThemeSwitcher from './ThemeSwitcher.vue'

let dispose = () => {}

function stubMatchMedia(reducedMotion: boolean) {
  vi.stubGlobal('matchMedia', (query: string) =>
    Object.assign(new EventTarget(), {
      matches: query.includes('reduce') ? reducedMotion : false,
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
    }),
  )
}

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  localStorage.clear()
  vi.unstubAllGlobals()
})

function mountSwitcher() {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () => h(ThemeSwitcher))
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const button = host.querySelector<HTMLButtonElement>('button.theme-switcher')
  if (!button) throw new Error('Missing theme switcher button')
  const icons = [...host.querySelectorAll<HTMLElement>('.theme-icon')]
  const sun = icons[0]
  const moon = icons[1]
  if (!sun || !moon) throw new Error('Missing theme icons')
  return { button, sun, moon }
}

function keyframeEffect(animation: Animation) {
  if (!(animation.effect instanceof KeyframeEffect))
    throw new Error('Expected a native KeyframeEffect')
  return animation.effect
}

function animationsFor(element: HTMLElement) {
  const animations = element.getAnimations()
  expect(animations).not.toHaveLength(0)
  expect(
    animations.every((animation) => animation.playState === 'running'),
  ).toBe(true)
  return animations.map((animation) => ({
    animation,
    effect: keyframeEffect(animation),
  }))
}

async function finishAnimations(...elements: HTMLElement[]) {
  for (const element of elements) {
    for (const animation of [...element.getAnimations()]) animation.finish()
  }
  await Promise.resolve()
  await Promise.resolve()
}

describe('ThemeSwitcher', () => {
  it('starts native opacity and ordered transform effects in both icon directions', async () => {
    stubMatchMedia(false)
    const { button, sun, moon } = mountSwitcher()

    button.click()
    await nextTick()

    const sunAnimations = animationsFor(sun)
    const moonAnimations = animationsFor(moon)
    expect(sunAnimations).toHaveLength(2)
    expect(moonAnimations).toHaveLength(2)

    const sunTransform = sunAnimations.find(({ effect }) =>
      effect.getKeyframes().some((keyframe) => 'transform' in keyframe),
    )
    const moonTransform = moonAnimations.find(({ effect }) =>
      effect.getKeyframes().some((keyframe) => 'transform' in keyframe),
    )
    if (!sunTransform || !moonTransform)
      throw new Error('Missing transform effect')
    expect(sunTransform.effect.getKeyframes()).toMatchObject([
      {
        transform: 'rotate(0deg) scale(1)',
      },
      {
        transform: 'rotate(45deg) scale(0.65)',
      },
    ])
    expect(moonTransform.effect.getKeyframes()).toMatchObject([
      {
        transform: 'rotate(-35deg) scale(0.65)',
      },
      {
        transform: 'rotate(0deg) scale(1)',
      },
    ])
    expect(sunTransform.effect.getTiming()).toMatchObject({ duration: 160 })
    expect(moonTransform.effect.getTiming()).toMatchObject({ duration: 160 })

    await finishAnimations(sun, moon)
    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expect(sun.style.opacity).toBe('')
    expect(sun.style.transform).toBe('')
    expect(moon.style.opacity).toBe('')
    expect(moon.style.transform).toBe('')
  })

  it('keeps reduced motion as one running opacity effect per icon', async () => {
    stubMatchMedia(true)
    const { button, sun, moon } = mountSwitcher()

    button.click()
    await nextTick()

    for (const icon of [sun, moon]) {
      const animations = animationsFor(icon)
      expect(animations).toHaveLength(1)
      expect(animations[0]?.effect.getKeyframes()).toMatchObject([
        { opacity: expect.any(String) },
        { opacity: expect.any(String) },
      ])
      expect(
        animations[0]?.effect
          .getKeyframes()
          .every((keyframe) => !('transform' in keyframe)),
      ).toBe(true)
      expect(icon.style.transform).toBe('')
    }

    await finishAnimations(sun, moon)
    expect(sun.style.opacity).toBe('')
    expect(moon.style.opacity).toBe('')
  })
})
