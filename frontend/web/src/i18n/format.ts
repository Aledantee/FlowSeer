import { useI18n } from 'vue-i18n'

export type Unit = 'celsius' | 'dbm' | 'gb' | 'gbps' | 'mbps' | 'ms' | 'w'

const MINUTES_PER_HOUR = 60
const MINUTES_PER_DAY = 1440
const MBPS_PER_GBPS = 1000

// Formats values for the active locale. Every call reads the global Composer,
// so output follows a locale switch without a remount.
export function useFormat() {
  const { t, n, d, locale } = useI18n({ useScope: 'global' })

  // A unit label is a message because Intl prints `Mb/s` for megabits in
  // every locale.
  function quantity(value: number, unit: Unit): string {
    return t('view.common.valueWithUnit', {
      value: n(value, 'decimal'),
      unit: t(`view.common.units.${unit}`),
    })
  }

  // Megabits per second. `scaled` switches to gigabits from 1000.
  function rate(mbps: number, scaled = false): string {
    if (scaled && mbps >= MBPS_PER_GBPS) {
      return quantity(mbps / MBPS_PER_GBPS, 'gbps')
    }
    return quantity(mbps, 'mbps')
  }

  // The compact link speed, `10G` or `100M`. No speed reads as empty.
  function speed(mbps: number | undefined): string {
    if (!mbps) return ''
    return mbps >= MBPS_PER_GBPS
      ? t('view.common.speed.gigabit', {
          value: n(mbps / MBPS_PER_GBPS, 'decimal'),
        })
      : t('view.common.speed.megabit', { value: n(mbps, 'decimal') })
  }

  // The count is formatted for display, and the raw count selects the plural
  // form: a string `count` would leave selection to the index argument.
  function counted(key: string, count: number): string {
    return t(key, { count: n(count, 'integer') }, count)
  }

  function facts(parts: ReadonlyArray<string | false | null | undefined>) {
    return parts.filter(Boolean).join(t('view.common.factSeparator'))
  }

  function ago(minutes: number): string {
    const relative = new Intl.RelativeTimeFormat(locale.value, {
      numeric: 'auto',
      style: 'short',
    })
    if (minutes < 1) return relative.format(0, 'second')
    if (minutes < MINUTES_PER_HOUR) {
      return relative.format(-Math.floor(minutes), 'minute')
    }
    if (minutes < MINUTES_PER_DAY) {
      return relative.format(-Math.floor(minutes / MINUTES_PER_HOUR), 'hour')
    }
    return relative.format(-Math.floor(minutes / MINUTES_PER_DAY), 'day')
  }

  function clock(date: Date | number): string {
    return d(date, 'time')
  }

  return { quantity, rate, speed, counted, facts, ago, clock }
}
