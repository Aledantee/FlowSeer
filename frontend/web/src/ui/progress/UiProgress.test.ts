// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
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
  localeOrI18n: WebLocale | ReturnType<typeof createWebI18n> = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiProgress, props)
    },
  })
  if (typeof localeOrI18n === 'string') {
    app.use(createWebI18n(localeOrI18n))
  } else {
    app.use(localeOrI18n)
  }
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

    const { el: elEnCustomMax } = mountProgress(
      { modelValue: 35, max: 50 },
      'en',
    )
    expect(elEnCustomMax.getAttribute('aria-valuetext')).toBe('70%')

    dispose()
    document.body.replaceChildren()

    const { el: elEnZero } = mountProgress({ modelValue: 0 }, 'en')
    expect(elEnZero.getAttribute('aria-valuetext')).toBe('0%')

    dispose()
    document.body.replaceChildren()

    const { el: elDe } = mountProgress({ modelValue: 45, max: 100 }, 'de')
    expect(elDe.getAttribute('aria-label')).toBe('Fortschritt')
    expect(elDe.getAttribute('aria-valuetext')).toBe('45\u00a0%')

    dispose()
    document.body.replaceChildren()

    const { el: elDeCustomMax } = mountProgress(
      { modelValue: 35, max: 50 },
      'de',
    )
    expect(elDeCustomMax.getAttribute('aria-valuetext')).toBe('70\u00a0%')
  })

  it('updates progress aria-label and valuetext on live locale change and preserves explicit overrides', async () => {
    const i18n = createWebI18n('en')
    const { el } = mountProgress({ modelValue: 45, max: 100 }, i18n)

    expect(el.getAttribute('aria-label')).toBe('Progress')
    expect(el.getAttribute('aria-valuetext')).toBe('45%')

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(el.getAttribute('aria-label')).toBe('Fortschritt')
    expect(el.getAttribute('aria-valuetext')).toBe('45\u00a0%')

    dispose()
    document.body.replaceChildren()

    const { el: elOverride } = mountProgress(
      {
        modelValue: 45,
        max: 100,
        ariaLabel: 'Custom Progress',
        valueText: 'Custom Value',
      },
      i18n,
    )

    expect(elOverride.getAttribute('aria-label')).toBe('Custom Progress')
    expect(elOverride.getAttribute('aria-valuetext')).toBe('Custom Value')

    i18n.global.locale.value = 'en'
    await nextTick()

    expect(elOverride.getAttribute('aria-label')).toBe('Custom Progress')
    expect(elOverride.getAttribute('aria-valuetext')).toBe('Custom Value')
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
