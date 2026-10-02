// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h } from 'vue'
import UiProgress from './UiProgress.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountProgress(
  props: Record<string, unknown> = {},
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiProgress, props)
    },
  })
  app.use(createWebI18n(locale))
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiProgress', () => {
  it('determinate progress sets aria-valuenow on ProgressRoot and applies transform style', () => {
    const { el } = mountProgress({ modelValue: 45, max: 100 })
    expect(el.getAttribute('aria-valuenow')).toBe('45')
    expect(el.getAttribute('aria-valuemax')).toBe('100')
    const indicator = el.querySelector('[data-max]') as HTMLElement
    expect(indicator).not.toBeNull()
    expect(indicator.style.transform).toContain('translateX(-55%)')
    expect(indicator.className).not.toContain('animate-progress-slide')
    expect(indicator.className).not.toContain('animate-pulse')
  })

  it('indeterminate progress sets data-state="indeterminate" and applies sliding animation', () => {
    const { el } = mountProgress({ modelValue: null })
    expect(el.getAttribute('data-state')).toBe('indeterminate')
    expect(el.getAttribute('aria-valuenow')).toBeNull()
    const indicator = el.querySelector('[data-max]') as HTMLElement
    expect(indicator).not.toBeNull()
    expect(indicator.className).toContain('animate-progress-slide')
    expect(indicator.className).not.toContain('animate-pulse')
    expect(indicator.className).toContain('motion-reduce:animate-none')
  })

  it('renders localized aria-label and aria-valuetext in en and de', () => {
    const { el: elEn } = mountProgress({ modelValue: 45, max: 100 }, 'en')
    expect(elEn.getAttribute('aria-label')).toBe('Progress')
    expect(elEn.getAttribute('aria-valuetext')).toBe('45%')

    dispose()
    document.body.replaceChildren()

    const { el: elDe } = mountProgress({ modelValue: 45, max: 100 }, 'de')
    expect(elDe.getAttribute('aria-label')).toBe('Fortschritt')
    expect(elDe.getAttribute('aria-valuetext')).toBe('45\u00a0%')
  })

  it('preserves explicit ariaLabel and valueText overrides across locales, including empty strings', () => {
    const { el: elCustom } = mountProgress(
      {
        modelValue: 45,
        max: 100,
        ariaLabel: 'Buffer Fill Level',
        valueText: (val: number | null, max: number) =>
          `${val} of ${max} packets`,
      },
      'de',
    )
    expect(elCustom.getAttribute('aria-label')).toBe('Buffer Fill Level')
    expect(elCustom.getAttribute('aria-valuetext')).toBe('45 of 100 packets')

    dispose()
    document.body.replaceChildren()

    const { el: elEmpty } = mountProgress(
      {
        modelValue: 45,
        max: 100,
        ariaLabel: '',
        valueText: '',
      },
      'de',
    )
    expect(elEmpty.getAttribute('aria-label')).toBe('')
    expect(elEmpty.getAttribute('aria-valuetext')).toBe('')
  })

  it('indeterminate progress sets aria-valuetext to undefined', () => {
    const { el } = mountProgress({ modelValue: null, max: 100 }, 'en')
    expect(el.getAttribute('aria-valuetext')).toBeNull()
  })
})
