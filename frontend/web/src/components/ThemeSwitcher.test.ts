// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { UiAppRoot } from '../ui'
import ThemeSwitcher from './ThemeSwitcher.vue'
import { createWebI18n } from '../i18n'

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
  app.use(createWebI18n())
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

function motionEffectsFor(element: HTMLElement) {
  const animations = animationsFor(element)
  expect(animations).toHaveLength(2)
  const opacity = animations.find(({ effect }) =>
    effect.getKeyframes().some((keyframe) => 'opacity' in keyframe),
  )
  const transform = animations.find(({ effect }) =>
    effect.getKeyframes().some((keyframe) => 'transform' in keyframe),
  )
  if (!opacity || !transform) throw new Error('Missing expected motion effect')
  return { opacity, transform }
}

function expectEmptyStyles(...elements: HTMLElement[]) {
  for (const element of elements) {
    expect(element.style.opacity).toBe('')
    expect(element.style.transform).toBe('')
  }
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

    const darkSun = motionEffectsFor(sun)
    const darkMoon = motionEffectsFor(moon)

    expect(darkSun.opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '1' },
      { opacity: '0' },
    ])
    expect(darkSun.transform.effect.getKeyframes()).toMatchObject([
      { transform: 'rotate(0deg) scale(1)' },
      { transform: 'rotate(45deg) scale(0.65)' },
    ])
    expect(darkSun.opacity.effect.getTiming()).toMatchObject({ duration: 160 })
    expect(darkSun.transform.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    expect(darkMoon.opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '0' },
      { opacity: '1' },
    ])
    expect(darkMoon.transform.effect.getKeyframes()).toMatchObject([
      { transform: 'rotate(-35deg) scale(0.65)' },
      { transform: 'rotate(0deg) scale(1)' },
    ])
    expect(darkMoon.opacity.effect.getTiming()).toMatchObject({ duration: 160 })
    expect(darkMoon.transform.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    await finishAnimations(sun, moon)
    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)

    button.click()
    await nextTick()

    const lightSun = motionEffectsFor(sun)
    const lightMoon = motionEffectsFor(moon)

    expect(lightSun.opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '0' },
      { opacity: '1' },
    ])
    expect(lightSun.transform.effect.getKeyframes()).toMatchObject([
      { transform: 'rotate(-45deg) scale(0.65)' },
      { transform: 'rotate(0deg) scale(1)' },
    ])
    expect(lightSun.opacity.effect.getTiming()).toMatchObject({ duration: 160 })
    expect(lightSun.transform.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    expect(lightMoon.opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '1' },
      { opacity: '0' },
    ])
    expect(lightMoon.transform.effect.getKeyframes()).toMatchObject([
      { transform: 'rotate(0deg) scale(1)' },
      { transform: 'rotate(35deg) scale(0.65)' },
    ])
    expect(lightMoon.opacity.effect.getTiming()).toMatchObject({
      duration: 160,
    })
    expect(lightMoon.transform.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    await finishAnimations(sun, moon)
    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)
  })

  it('keeps reduced motion as one running opacity effect per icon', async () => {
    stubMatchMedia(true)
    const { button, sun, moon } = mountSwitcher()

    button.click()
    await nextTick()

    for (const icon of [sun, moon]) {
      const animations = animationsFor(icon)
      expect(animations).toHaveLength(1)
      expect(
        animations[0]?.effect
          .getKeyframes()
          .every((keyframe) => !('transform' in keyframe)),
      ).toBe(true)
      expect(icon.style.transform).toBe('')
    }

    const darkSunAnimations = animationsFor(sun)
    const darkMoonAnimations = animationsFor(moon)
    expect(darkSunAnimations[0]?.effect.getKeyframes()).toMatchObject([
      { opacity: '1' },
      { opacity: '0' },
    ])
    expect(darkMoonAnimations[0]?.effect.getKeyframes()).toMatchObject([
      { opacity: '0' },
      { opacity: '1' },
    ])
    expect(darkSunAnimations[0]?.effect.getTiming()).toMatchObject({
      duration: 160,
    })
    expect(darkMoonAnimations[0]?.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    await finishAnimations(sun, moon)
    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)

    button.click()
    await nextTick()

    for (const icon of [sun, moon]) {
      const animations = animationsFor(icon)
      expect(animations).toHaveLength(1)
      expect(
        animations[0]?.effect
          .getKeyframes()
          .every((keyframe) => !('transform' in keyframe)),
      ).toBe(true)
      expect(icon.style.transform).toBe('')
    }

    const lightSunAnimations = animationsFor(sun)
    const lightMoonAnimations = animationsFor(moon)
    expect(lightSunAnimations[0]?.effect.getKeyframes()).toMatchObject([
      { opacity: '0' },
      { opacity: '1' },
    ])
    expect(lightMoonAnimations[0]?.effect.getKeyframes()).toMatchObject([
      { opacity: '1' },
      { opacity: '0' },
    ])
    expect(lightSunAnimations[0]?.effect.getTiming()).toMatchObject({
      duration: 160,
    })
    expect(lightMoonAnimations[0]?.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    await finishAnimations(sun, moon)
    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)
  })

  it('replaces running effects on rapid toggles and restores empty inline styles on finish', async () => {
    stubMatchMedia(false)
    const { button, sun, moon } = mountSwitcher()

    button.click()
    await nextTick()
    const firstSunAnimations = animationsFor(sun)
    const firstMoonAnimations = animationsFor(moon)

    button.click()
    await nextTick()

    expect(
      firstSunAnimations.every(
        ({ animation }) => animation.playState === 'idle',
      ),
    ).toBe(true)
    expect(
      firstMoonAnimations.every(
        ({ animation }) => animation.playState === 'idle',
      ),
    ).toBe(true)

    button.click()
    await nextTick()

    const latestSun = motionEffectsFor(sun)
    const latestMoon = motionEffectsFor(moon)

    expect(latestSun.opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '1' },
      { opacity: '0' },
    ])
    expect(latestSun.transform.effect.getKeyframes()).toMatchObject([
      { transform: 'rotate(0deg) scale(1)' },
      { transform: 'rotate(45deg) scale(0.65)' },
    ])
    expect(latestSun.opacity.effect.getTiming()).toMatchObject({
      duration: 160,
    })
    expect(latestSun.transform.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    expect(latestMoon.opacity.effect.getKeyframes()).toMatchObject([
      { opacity: '0' },
      { opacity: '1' },
    ])
    expect(latestMoon.transform.effect.getKeyframes()).toMatchObject([
      { transform: 'rotate(-35deg) scale(0.65)' },
      { transform: 'rotate(0deg) scale(1)' },
    ])
    expect(latestMoon.opacity.effect.getTiming()).toMatchObject({
      duration: 160,
    })
    expect(latestMoon.transform.effect.getTiming()).toMatchObject({
      duration: 160,
    })

    await finishAnimations(sun, moon)
    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)
  })

  it('restores empty inline styles on window resize during rapid toggle', async () => {
    stubMatchMedia(false)
    const { button, sun, moon } = mountSwitcher()

    button.click()
    await nextTick()
    button.click()
    await nextTick()

    expect(animationsFor(sun)).toHaveLength(2)
    expect(animationsFor(moon)).toHaveLength(2)

    sun.style.opacity = '0.5'
    sun.style.transform = 'rotate(9deg) scale(0.9)'
    moon.style.opacity = '0.5'
    moon.style.transform = 'rotate(9deg) scale(0.9)'
    window.dispatchEvent(new Event('resize'))
    await nextTick()

    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)
  })

  it('restores empty inline styles on unmount during rapid toggle', async () => {
    stubMatchMedia(false)
    const { button, sun, moon } = mountSwitcher()

    button.click()
    await nextTick()
    button.click()
    await nextTick()

    expect(animationsFor(sun)).toHaveLength(2)
    expect(animationsFor(moon)).toHaveLength(2)

    sun.style.opacity = '0.5'
    sun.style.transform = 'rotate(9deg) scale(0.9)'
    moon.style.opacity = '0.5'
    moon.style.transform = 'rotate(9deg) scale(0.9)'
    dispose()
    await nextTick()

    expect(sun.getAnimations()).toHaveLength(0)
    expect(moon.getAnimations()).toHaveLength(0)
    expectEmptyStyles(sun, moon)
  })
})
