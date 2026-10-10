// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h } from 'vue'
import TrafficChart from './TrafficChart.vue'
import { createWebI18n, type WebLocale } from '../i18n'

let dispose = () => {}

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
})

function mountChart(
  locale: WebLocale,
  points = [
    { hour: 14, mbps: 100 },
    { hour: 15, mbps: 200 },
  ],
) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n(locale)
  const app = createApp({
    render() {
      return h(TrafficChart, {
        points,
        label: 'Network Traffic',
      })
    },
  })
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('TrafficChart', () => {
  it.each(['en', 'de'] as const)(
    'formats chart hour labels according to Intl.DateTimeFormat in %s',
    (locale) => {
      const hour = 14
      const expected = new Intl.DateTimeFormat(locale, {
        hour: 'numeric',
        minute: '2-digit',
      }).format(new Date(2000, 0, 1, hour))

      const host = mountChart(locale, [
        { hour, mbps: 50 },
        { hour: 15, mbps: 80 },
      ])

      const texts = Array.from(host.querySelectorAll('.traffic-grid text')).map(
        (el) => el.textContent?.trim(),
      )
      if (texts.length === 0) throw new Error('Missing hour labels in chart')

      expect(texts).toContain(expected)
    },
  )

  it('renders zero traffic with a finite non-zero scale and no NaN', () => {
    const host = mountChart('en', [
      { hour: 0, mbps: 0 },
      { hour: 1, mbps: 0 },
    ])

    const texts = Array.from(host.querySelectorAll('.traffic-grid text')).map(
      (el) => el.textContent?.trim(),
    )
    expect(texts.some((t) => t?.includes('NaN'))).toBe(false)
    // Finite scale ceiling should be at least 1, not 0 or NaN
    expect(texts).toContain('1')

    const tableCells = Array.from(host.querySelectorAll('table tbody td')).map(
      (el) => el.textContent?.trim(),
    )
    expect(tableCells).toContain('0')
  })

  it('scales high rates to localized gigabits and updates accessible table to match plot', () => {
    const host = mountChart('en', [
      { hour: 10, mbps: 2400 },
      { hour: 11, mbps: 1800 },
    ])

    // Accessible table header must express the scaled gigabit unit
    const tableHeader = host.querySelector(
      'table thead th:last-child',
    )?.textContent
    expect(tableHeader).toContain('Gbit/s')

    // Table rows must contain scaled values (2.4, 1.8) rather than unscaled mbps (2,400, 1,800)
    const tableCells = Array.from(host.querySelectorAll('table tbody td')).map(
      (el) => el.textContent?.trim(),
    )
    expect(tableCells).toContain('2.4')
    expect(tableCells).toContain('1.8')
    expect(tableCells).not.toContain('2,400')

    // SVG grid labels must be in scaled units (e.g. 5 or 2.5), not unscaled thousands
    const gridTexts = Array.from(
      host.querySelectorAll('.traffic-grid text'),
    ).map((el) => el.textContent?.trim())
    expect(gridTexts.some((t) => t?.includes('2,400'))).toBe(false)
  })

  it('chooses time-label density including both first sample and last sample', () => {
    const sixPoints = [
      { hour: 8, mbps: 100 },
      { hour: 9, mbps: 120 },
      { hour: 10, mbps: 140 },
      { hour: 11, mbps: 160 },
      { hour: 12, mbps: 180 },
      { hour: 13, mbps: 200 },
    ]
    const host = mountChart('en', sixPoints)
    const gridTexts = Array.from(
      host.querySelectorAll('.traffic-grid text'),
    ).map((el) => el.textContent?.trim())
    // First sample (8:00 AM in en) and last sample ("Now") must be present
    const firstExpected = new Intl.DateTimeFormat('en', {
      hour: 'numeric',
      minute: '2-digit',
    }).format(new Date(2000, 0, 1, 8))
    expect(gridTexts).toContain(firstExpected)
    expect(gridTexts).toContain('Now')
  })
})
