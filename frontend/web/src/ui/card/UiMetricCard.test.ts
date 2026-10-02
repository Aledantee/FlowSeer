// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, type Component } from 'vue'
import UiMetricCard from './UiMetricCard.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountCard(
  props: Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiMetricCard as Component, props, slots)
    },
  })
  const i18n = createWebI18n(locale)
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
})
