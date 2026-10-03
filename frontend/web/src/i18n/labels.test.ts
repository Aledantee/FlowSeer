// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp } from 'vue'
import { createWebI18n, type WebLocale } from './index'
import { useLabels } from './labels'
import type { PageView } from '../navigation/page'

const disposers: Array<() => void> = []

afterEach(() => {
  while (disposers.length) disposers.pop()?.()
})

function mountLabels(locale: WebLocale) {
  const i18n = createWebI18n(locale)
  let labels!: ReturnType<typeof useLabels>
  const app = createApp({
    setup() {
      labels = useLabels()
      return () => null
    },
  })
  app.use(i18n)
  app.mount(document.createElement('div'))
  disposers.push(() => app.unmount())
  return {
    labels,
    setLocale(next: WebLocale) {
      i18n.global.locale.value = next
    },
  }
}

const PAGES: ReadonlyArray<[PageView, string, string]> = [
  ['dashboard', 'Dashboard', 'Dashboard'],
  ['devices', 'Devices', 'Geräte'],
  ['clients', 'Clients', 'Clients'],
  ['sites', 'Sites', 'Standorte'],
  ['topology', 'Topology', 'Topologie'],
  ['device', 'Device', 'Gerät'],
]

describe('useLabels', () => {
  it.each(PAGES)('names the %s page in both locales', (id, en, de) => {
    expect(mountLabels('en').labels.page(id)).toBe(en)
    expect(mountLabels('de').labels.page(id)).toBe(de)
  })

  it('names every identifier value in both locales', () => {
    const cases = (labels: ReturnType<typeof useLabels>) => ({
      health: (['Healthy', 'Degraded', 'Offline'] as const).map(labels.health),
      severity: (['critical', 'warning', 'info'] as const).map(labels.severity),
      reachability: (['Reachable', 'Unreachable'] as const).map(
        labels.reachability,
      ),
      lifecycle: (['Active', 'Retired'] as const).map(labels.lifecycle),
      portStatus: (['Up', 'Down', 'Disabled'] as const).map(labels.portStatus),
      band: (['2.4 GHz', '5 GHz', '6 GHz'] as const).map(labels.band),
      medium: (['Fiber', 'Copper'] as const).map(labels.medium),
      mode: (['Trunk', 'Access', 'Routed'] as const).map(labels.mode),
      duplex: (['Full', 'Half'] as const).map(labels.duplex),
      signal: (['Strong', 'Fair', 'Weak'] as const).map(labels.signal),
    })

    expect(cases(mountLabels('en').labels)).toEqual({
      health: ['Healthy', 'Degraded', 'Offline'],
      severity: ['Critical', 'Warning', 'Info'],
      reachability: ['Reachable', 'Unreachable'],
      lifecycle: ['Active', 'Retired'],
      portStatus: ['Up', 'Down', 'Disabled'],
      band: ['2.4 GHz', '5 GHz', '6 GHz'],
      medium: ['Fiber', 'Copper'],
      mode: ['Trunk', 'Access', 'Routed'],
      duplex: ['Full duplex', 'Half duplex'],
      signal: ['Strong', 'Fair', 'Weak'],
    })
    expect(cases(mountLabels('de').labels)).toEqual({
      health: ['Gesund', 'Beeinträchtigt', 'Offline'],
      severity: ['Kritisch', 'Warnung', 'Info'],
      reachability: ['Erreichbar', 'Nicht erreichbar'],
      lifecycle: ['Aktiv', 'Ausgemustert'],
      portStatus: ['Verbunden', 'Getrennt', 'Deaktiviert'],
      band: ['2,4 GHz', '5 GHz', '6 GHz'],
      medium: ['Glasfaser', 'Kupfer'],
      mode: ['Trunk', 'Zugang', 'Geroutet'],
      duplex: ['Vollduplex', 'Halbduplex'],
      signal: ['Stark', 'Mäßig', 'Schwach'],
    })
  })

  it('builds the health line from the offline and degraded counts', () => {
    const en = mountLabels('en').labels
    const de = mountLabels('de').labels
    const one = { Healthy: 3, Degraded: 0, Offline: 1 }
    const two = { Healthy: 3, Degraded: 2, Offline: 1234 }
    const none = { Healthy: 4, Degraded: 0, Offline: 0 }

    expect(en.healthLine(one)).toBe('1 offline')
    expect(de.healthLine(one)).toBe('1 offline')
    expect(en.healthLine(two)).toBe('1,234 offline · 2 degraded')
    expect(de.healthLine(two)).toBe('1.234 offline · 2 beeinträchtigt')
    expect(en.healthLine(none)).toBe('All healthy')
    expect(de.healthLine(none)).toBe('Alles gesund')
  })

  it('follows a locale switch on the same instance', () => {
    const { labels, setLocale } = mountLabels('en')
    const line = { Healthy: 1, Degraded: 2, Offline: 0 }
    expect(labels.page('sites')).toBe('Sites')
    expect(labels.health('Offline')).toBe('Offline')
    expect(labels.band('2.4 GHz')).toBe('2.4 GHz')
    expect(labels.healthLine(line)).toBe('2 degraded')

    setLocale('de')
    expect(labels.page('sites')).toBe('Standorte')
    expect(labels.health('Degraded')).toBe('Beeinträchtigt')
    expect(labels.band('2.4 GHz')).toBe('2,4 GHz')
    expect(labels.healthLine(line)).toBe('2 beeinträchtigt')

    setLocale('en')
    expect(labels.page('sites')).toBe('Sites')
  })

  it('holds the glossary values the views share', () => {
    const en = createWebI18n('en').global
    const de = createWebI18n('de').global
    expect(en.t('view.common.allTenants')).toBe('All tenants')
    expect(de.t('view.common.allTenants')).toBe('Alle Mandanten')
    expect(en.t('view.common.allSites')).toBe('All sites')
    expect(de.t('view.common.allSites')).toBe('Alle Standorte')
    for (const [key, count, expected] of [
      ['view.common.devices', 1, '1 Gerät'],
      ['view.common.devices', 16, '16 Geräte'],
      ['view.common.results', 1, '1 Ergebnis'],
      ['view.common.results', 16, '16 Ergebnisse'],
    ] as const) {
      expect(de.t(key, { count: de.n(count, 'integer') }, count)).toBe(expected)
    }
    expect(en.t('view.common.results', { count: '1' }, 1)).toBe('1 result')
    expect(en.t('view.common.results', { count: '16' }, 16)).toBe('16 results')
    expect(en.t('view.common.unknownSite')).toBe('Unknown site')
    expect(en.t('view.common.unknownTenant')).toBe('Unknown tenant')
    expect(en.t('view.common.unknownDevice')).toBe('Unknown device')
  })
})
