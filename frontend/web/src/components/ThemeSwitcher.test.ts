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
  const icons = host.querySelectorAll<HTMLElement>('.theme-icon')
  const sun = icons[0]
  const moon = icons[1]
  if (!sun || !moon) throw new Error('Missing theme icons')
  return { button, sun, moon }
}

function wait(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

describe('ThemeSwitcher', () => {
  it('animates theme icons during normal motion and clears styles after completion', async () => {
    stubMatchMedia(false)
    const { button, sun, moon } = mountSwitcher()

    expect(button.getAttribute('aria-checked')).toBe('false')

    button.click()
    await nextTick()

    expect(button.getAttribute('aria-checked')).toBe('true')

    await wait(40)
    expect(sun.style.transform).toContain('rotate')

    await wait(280)
    expect(sun.style.transform).toBe('')
    expect(sun.style.opacity).toBe('')
    expect(moon.style.transform).toBe('')
    expect(moon.style.opacity).toBe('')
  })

  it('keeps transforms empty and retains running opacity animations under reduced motion', async () => {
    stubMatchMedia(true)
    const { button, sun, moon } = mountSwitcher()

    expect(button.getAttribute('aria-checked')).toBe('false')

    button.click()
    await nextTick()

    expect(button.getAttribute('aria-checked')).toBe('true')

    await wait(40)
    expect(sun.style.transform).toBe('')
    expect(moon.style.transform).toBe('')
    expect(sun.getAnimations()).toHaveLength(1)
    expect(moon.getAnimations()).toHaveLength(1)
  })
})
