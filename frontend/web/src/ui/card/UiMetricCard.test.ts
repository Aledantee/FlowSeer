// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiMetricCard, { type UiMetricCardProps } from './UiMetricCard.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountCard(
  props: UiMetricCardProps,
  slots: Record<string, () => unknown> = {},
  locale: WebLocale = 'en',
  messages?: Record<string, unknown>,
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiMetricCard, props, slots)
    },
  })
  const i18n = createWebI18n(locale)
  if (messages) i18n.global.mergeLocaleMessage('en', messages)
  app.use(i18n)
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  return { host, i18n }
}

describe('UiMetricCard', () => {
  it('renders label, value with tabular figures, icon slot, and note slot content', () => {
    const { host } = mountCard(
      {
        label: 'Total Bandwidth',
        value: 120,
        unit: 'Gbps',
      },
      {
        icon: () => h('span', { class: 'custom-icon' }, 'ICON'),
        default: () => 'Steady over 24h',
      },
    )

    expect(host.textContent).toContain('Total Bandwidth')
    expect(host.textContent).toContain('120')
    expect(host.textContent).toContain('Gbps')
    expect(host.textContent).toContain('ICON')
    expect(host.textContent).toContain('Steady over 24h')

    const valueEl = host.querySelector('.metric-value')
    expect(valueEl).not.toBeNull()
    expect(valueEl?.className).toContain('font-mono')
    expect(valueEl?.className).toContain('tabular-nums')

    const iconEl = host.querySelector('.custom-icon')
    expect(iconEl).not.toBeNull()
  })

  it('formats 1234.5 as 1.234,5 in de with a non-breaking space before unit', () => {
    const { host } = mountCard(
      {
        label: 'Durchsatz',
        value: 1234.5,
        unit: 'Gbps',
      },
      {},
      'de',
    )

    const valueEl = host.querySelector('.metric-value')
    expect(valueEl?.textContent?.trim()).toBe('1.234,5')
    expect(host.textContent).toContain('1.234,5\u00a0Gbps')
  })

  it('takes the value and unit format from the catalog', () => {
    const { host } = mountCard(
      { label: 'Throughput', value: 120, unit: 'Gbps' },
      {},
      'en',
      { ui: { metricCard: { valueWithUnit: '{value}/{unit}' } } },
    )
    expect(host.querySelector('article')?.textContent).toContain('120/Gbps')
  })

  it('allows valueText formatter to override complete display while preserving metric-value selector', () => {
    const { host } = mountCard({
      label: 'Throughput',
      value: 1234.5,
      unit: 'Gbps',
      valueText: (value: number, unit?: string) => `Custom ${value} ${unit}`,
    })

    const valueEl = host.querySelector('.metric-value')
    expect(valueEl?.textContent?.trim()).toBe('Custom 1234.5 Gbps')
    expect(host.textContent).toContain('Custom 1234.5 Gbps')
  })

  it('formats 1234.5 as 1.234,5 in de without a unit', () => {
    const { host } = mountCard({ label: 'Durchsatz', value: 1234.5 }, {}, 'de')

    const valueEl = host.querySelector('.metric-value')
    expect(valueEl?.textContent?.trim()).toBe('1.234,5')
  })

  it('renders a small fraction unrounded in en and de, with and without a unit', () => {
    const cases: { locale: WebLocale; expected: string }[] = [
      { locale: 'en', expected: '0.0004' },
      { locale: 'de', expected: '0,0004' },
    ]
    for (const { locale, expected } of cases) {
      const bare = mountCard({ label: 'Loss', value: 0.0004 }, {}, locale)
      expect(
        bare.host.querySelector('.metric-value')?.textContent?.trim(),
      ).toBe(expected)
      dispose()

      const withUnit = mountCard(
        { label: 'Loss', value: 0.0004, unit: 'ms' },
        {},
        locale,
      )
      expect(
        withUnit.host.querySelector('.metric-value')?.textContent?.trim(),
      ).toBe(expected)
      dispose()
    }
  })

  it('follows a live locale switch for the number format and keeps a valueText override', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const i18n = createWebI18n('en')
    const app = createApp({
      render() {
        return h('div', [
          h(UiMetricCard, { label: 'Bare', value: 1234.5 }),
          h(UiMetricCard, {
            label: 'Unit',
            value: 1234.5,
            unit: 'Gbps',
          }),
          h(UiMetricCard, {
            label: 'Custom',
            value: 1234.5,
            valueText: (value: number) => `Custom ${value}`,
          }),
        ])
      },
    })
    app.use(i18n)
    app.mount(host)
    dispose = () => {
      app.unmount()
      dispose = () => {}
    }

    const values = () =>
      Array.from(host.querySelectorAll('.metric-value')).map((el) =>
        el.textContent?.trim(),
      )
    expect(values()).toEqual(['1,234.5', '1,234.5', 'Custom 1234.5'])

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(values()).toEqual(['1.234,5', '1.234,5', 'Custom 1234.5'])
  })
})
