// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp } from 'vue'
import { createWebI18n, type WebLocale } from './index'
import { useFormat } from './format'

const NBSP = ' '
const disposers: Array<() => void> = []

afterEach(() => {
  while (disposers.length) disposers.pop()?.()
})

function mountFormat(locale: WebLocale) {
  const i18n = createWebI18n(locale)
  let format!: ReturnType<typeof useFormat>
  const app = createApp({
    setup() {
      format = useFormat()
      return () => null
    },
  })
  app.use(i18n)
  app.mount(document.createElement('div'))
  disposers.push(() => app.unmount())
  return {
    format,
    setLocale(next: WebLocale) {
      i18n.global.locale.value = next
    },
  }
}

function relative(
  locale: WebLocale,
  value: number,
  unit: Intl.RelativeTimeFormatUnit,
) {
  return new Intl.RelativeTimeFormat(locale, {
    numeric: 'auto',
    style: 'short',
  }).format(value, unit)
}

describe('useFormat', () => {
  it('joins a decimal value and its unit label with a non-breaking space', () => {
    expect(mountFormat('en').format.quantity(1234.5, 'mbps')).toBe(
      `1,234.5${NBSP}Mbit/s`,
    )
    expect(mountFormat('de').format.quantity(1234.5, 'mbps')).toBe(
      `1.234,5${NBSP}Mbit/s`,
    )
    expect(mountFormat('en').format.quantity(-61, 'dbm')).toBe(`-61${NBSP}dBm`)
  })

  it('scales a rate to gigabits only on request and from 1000', () => {
    const { format } = mountFormat('en')
    expect(format.rate(2500)).toBe(`2,500${NBSP}Mbit/s`)
    expect(format.rate(999, true)).toBe(`999${NBSP}Mbit/s`)
    expect(format.rate(1000, true)).toBe(`1${NBSP}Gbit/s`)
    expect(format.rate(2500, true)).toBe(`2.5${NBSP}Gbit/s`)
    expect(mountFormat('de').format.rate(2500, true)).toBe(`2,5${NBSP}Gbit/s`)
  })

  it('selects the plural form by the raw count and formats the count', () => {
    const en = mountFormat('en').format
    expect(en.counted('view.common.devices', 1)).toBe('1 device')
    expect(en.counted('view.common.devices', 2)).toBe('2 devices')
    expect(en.counted('view.common.devices', 1234)).toBe('1,234 devices')
    const de = mountFormat('de').format
    expect(de.counted('view.common.devices', 1)).toBe('1 Gerät')
    expect(de.counted('view.common.devices', 2)).toBe('2 Geräte')
    expect(de.counted('view.common.devices', 1234)).toBe('1.234 Geräte')
  })

  it('prints a compact link speed and nothing for no speed', () => {
    const en = mountFormat('en').format
    expect(en.speed(100)).toBe('100M')
    expect(en.speed(1000)).toBe('1G')
    expect(en.speed(10_000)).toBe('10G')
    expect(en.speed(2500)).toBe('2.5G')
    expect(en.speed(undefined)).toBe('')
    expect(en.speed(0)).toBe('')
    expect(mountFormat('de').format.speed(2500)).toBe('2,5G')
  })

  it('joins the present facts with the separator message', () => {
    const { format } = mountFormat('en')
    expect(
      format.facts(['eth0', undefined, 'Up', '', false, null, '10G']),
    ).toBe('eth0 · Up · 10G')
    expect(format.facts([undefined, ''])).toBe('')
  })

  it.each([
    [0, 0, 'second'],
    [0.5, 0, 'second'],
    [1, -1, 'minute'],
    [38, -38, 'minute'],
    [59.9, -59, 'minute'],
    [60, -1, 'hour'],
    [61, -1, 'hour'],
    [1439, -23, 'hour'],
    [1440, -1, 'day'],
    [3000, -2, 'day'],
  ] as const)(
    'prints %s minutes as a relative time of %s %s in each locale',
    (minutes, value, unit) => {
      for (const locale of ['en', 'de'] as const) {
        expect(mountFormat(locale).format.ago(minutes)).toBe(
          relative(locale, value, unit),
        )
      }
    },
  )

  it('prints a clock time with the locale hour cycle', () => {
    const at = new Date(2026, 9, 3, 14, 0)
    for (const locale of ['en', 'de'] as const) {
      expect(mountFormat(locale).format.clock(at)).toBe(
        new Intl.DateTimeFormat(locale, {
          hour: 'numeric',
          minute: '2-digit',
        }).format(at),
      )
    }
  })

  it('follows a locale switch on the same instance in every formatter', () => {
    const { format, setLocale } = mountFormat('en')
    const at = new Date(2026, 9, 3, 14, 0)
    const snapshot = () => ({
      quantity: format.quantity(1234.5, 'mbps'),
      rate: format.rate(2500, true),
      counted: format.counted('view.common.devices', 1234),
      speed: format.speed(2500),
      ago: format.ago(38),
      clock: format.clock(at),
    })

    const english = snapshot()
    expect(english).toMatchObject({
      quantity: `1,234.5${NBSP}Mbit/s`,
      rate: `2.5${NBSP}Gbit/s`,
      counted: '1,234 devices',
      speed: '2.5G',
      ago: relative('en', -38, 'minute'),
    })

    setLocale('de')
    expect(snapshot()).toMatchObject({
      quantity: `1.234,5${NBSP}Mbit/s`,
      rate: `2,5${NBSP}Gbit/s`,
      counted: '1.234 Geräte',
      speed: '2,5G',
      ago: relative('de', -38, 'minute'),
    })

    setLocale('en')
    expect(snapshot()).toEqual(english)
  })
})
