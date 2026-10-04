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
})
