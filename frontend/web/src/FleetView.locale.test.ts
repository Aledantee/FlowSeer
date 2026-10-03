// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { UiAppRoot } from './ui'
import { isMac } from './navigation/shortcuts'
import { DOCK_KEY } from './navigation/dock'
import * as fleetDomain from './domain/fleet'
import { devices, filterDevices } from './domain/fleet'
import { clientsOf, signalQuality } from './domain/clients'
import type { Port } from './domain/telemetry'
import DevicePorts from './components/DevicePorts.vue'
import { createAiRegistry, createAiTargetDirective } from './ai'
import { aiRegistryKey } from './ui/ai/context'
import { createWebI18n } from './i18n'
import type { WebLocale } from './i18n'
import { i18nWarnings } from './i18n/testing'
import enCatalog from './i18n/locales/en.json'
import deCatalog from './i18n/locales/de.json'

let dispose = () => {}
let warn: ReturnType<typeof vi.spyOn>

// Reduced motion keeps the end state static, and the `min-width` match keeps
// the desktop layout, without which `toggleSplit` returns early.
beforeEach(() => {
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('reduce') || query.includes('min-width'),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const collapsed =
        this instanceof HTMLElement &&
        this.closest('.shell')?.classList.contains('sidebar-collapsed')
      const width = collapsed ? 64 : 204
      return {
        bottom: 64,
        height: 64,
        left: 0,
        right: width,
        top: 0,
        width,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      } as DOMRect
    },
  )
})

afterEach(() => {
  dispose()
  dispose = () => {}
  localStorage.clear()
  sessionStorage.clear()
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function mountLocale(path: string, locale: WebLocale) {
  const host = document.createElement('div')
  document.body.append(host)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/:view(dashboard|devices|clients|sites|topology)',
        component: FleetView,
      },
      { path: '/devices/:deviceId', component: FleetView },
    ],
  })
  const registry = createAiRegistry()
  const i18n = createWebI18n(locale)
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () => h(FleetView))
    },
  })
  await router.push(path)
  app.use(i18n)
  app.use(router)
  app.directive('ai-target', createAiTargetDirective(registry))
  app.provide(aiRegistryKey, registry)
  await router.isReady()
  app.mount(host)
  dispose = () => app.unmount()
  await settle()
  return {
    host,
    router,
    registry,
    async setLocale(next: WebLocale) {
      i18n.global.locale.value = next
      await settle()
    },
  }
}

function toggleSplitShortcut() {
  return new KeyboardEvent('keydown', {
    key: '\\',
    code: 'Backslash',
    bubbles: true,
    cancelable: true,
    ...(isMac() ? { metaKey: true } : { ctrlKey: true }),
  })
}

const text = (elements: Iterable<Element>) =>
  [...elements].map((item) => item.textContent?.trim())
const labelsOf = (elements: Iterable<Element>) =>
  [...elements].map((item) => item.getAttribute('aria-label'))

describe('FleetView shell in German', () => {
  it('names the navigation, the title, and the sidebar toggle', async () => {
    const { host } = await mountLocale('/dashboard', 'de')

    expect(text(host.querySelectorAll('.nav-text'))).toEqual([
      'Dashboard',
      'Geräte',
      'Topologie',
      'Clients',
      'Standorte',
    ])
    const count = host.querySelector('.nav-count')?.textContent?.trim()
    expect(count).toBe(new Intl.NumberFormat('de').format(devices.length))
    expect(labelsOf(host.querySelectorAll('nav[aria-label] > a'))).toEqual([
      'Dashboard',
      `Geräte, ${count} im Bereich`,
      'Topologie',
      'Clients',
      'Standorte',
    ])
    expect(
      host.querySelector('nav[aria-label="Hauptnavigation"]'),
    ).not.toBeNull()
    expect(host.querySelector('.nav-label')?.textContent?.trim()).toBe(
      'ARBEITSBEREICH',
    )
    expect(host.querySelector('.skip-link')?.textContent).toBe(
      'Zum Hauptinhalt springen',
    )
    expect(document.title).toBe('Dashboard · FlowSeer')
    expect(
      host.querySelector('.sidebar-toggle')?.getAttribute('aria-label'),
    ).toBe('Seitenleiste ausblenden')
    expect(
      host.querySelector('.product-brand')?.getAttribute('translate'),
    ).toBe('no')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('labels the split controls', async () => {
    const { host } = await mountLocale('/dashboard', 'de')

    window.dispatchEvent(toggleSplitShortcut())
    await settle()

    expect(document.querySelector('.panes.split')).not.toBeNull()
    expect(document.title).toBe('Dashboard + Dashboard · FlowSeer')
    const tools = host.querySelector('.pane-tools')
    expect(tools?.getAttribute('aria-label')).toBe('Geteilte Ansicht')
    expect(labelsOf(tools?.querySelectorAll('button') ?? [])).toEqual([
      'Links der Hauptseite in der zweiten Seite öffnen',
      'Beide Seiten tauschen',
      'Beide Seiten als Paar im Dock ablegen',
      'Die zweite Seite in das Dock minimieren',
      'Die zweite Seite schließen',
    ])
    expect(
      host.querySelector('[role="separator"]')?.getAttribute('aria-label'),
    ).toBe('Größe der geteilten Ansicht anpassen')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('labels the dock', async () => {
    const offline = devices.find((device) => device.health === 'Offline')
    if (!offline) throw new Error('The fixture has no offline device')
    const attention = filterDevices(devices, '', '', '', 'attention').length
    expect(attention).toBeGreaterThan(0)
    localStorage.setItem(
      DOCK_KEY,
      JSON.stringify([
        { id: 'one', location: { path: '/devices', query: {} } },
        {
          id: 'two',
          location: { path: '/clients', query: {} },
          beside: { path: '/sites', query: {} },
        },
        {
          id: 'three',
          location: { path: `/devices/${offline.id}`, query: {} },
        },
      ]),
    )
    const { host } = await mountLocale('/dashboard', 'de')

    const dock = host.querySelector('.page-dock')
    expect(dock?.getAttribute('aria-label')).toBe('Minimierte Seiten')
    const tabs = [...(dock?.querySelectorAll('.dock-tab') ?? [])]
    expect(tabs.map((tab) => tab.querySelector('strong')?.textContent)).toEqual(
      ['Geräte', 'Clients + Standorte', offline.name],
    )
    expect(
      tabs.map((tab) => labelsOf(tab.querySelectorAll('.dock-action'))),
    ).toEqual([
      ['Geräte nebeneinander öffnen', 'Geräte schließen'],
      ['Clients + Standorte schließen'],
      [`${offline.name} nebeneinander öffnen`, `${offline.name} schließen`],
    ])
    expect(
      tabs.map((tab) =>
        tab.querySelector('.dock-badge')?.getAttribute('aria-label'),
      ),
    ).toEqual([
      `${attention} mit Handlungsbedarf`,
      `${attention * 2} mit Handlungsbedarf`,
      'Offline',
    ])
    expect(tabs[0]?.querySelector('small')?.textContent).toBe('Alle Standorte')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a locale switch on the same mount', async () => {
    const { host, router, setLocale } = await mountLocale('/dashboard', 'de')
    await router.push('/sites')
    await settle()
    expect(document.title).toBe('Standorte · FlowSeer')

    await setLocale('en')

    expect(text(host.querySelectorAll('.nav-text'))).toEqual([
      'Dashboard',
      'Devices',
      'Topology',
      'Clients',
      'Sites',
    ])
    expect(document.title).toBe('Sites · FlowSeer')
    expect(
      host.querySelector('.sidebar-toggle')?.getAttribute('aria-label'),
    ).toBe('Collapse sidebar')
    expect(host.querySelector('.nav-label')?.textContent?.trim()).toBe(
      'WORKSPACE',
    )

    await setLocale('de')

    expect(text(host.querySelectorAll('.nav-text'))).toContain('Standorte')
    expect(document.title).toBe('Standorte · FlowSeer')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

function topBarButton(host: HTMLElement, label: string) {
  const button = [
    ...host.querySelectorAll<HTMLButtonElement>('.topbar-tools button'),
  ].find((item) => item.getAttribute('aria-label') === label)
  if (!button) throw new Error(`Missing top bar button ${label}`)
  return button
}

const dialog = () => document.body.querySelector('[role="dialog"]')

describe('top bar dialogs in German', () => {
  it('opens the help dialog', async () => {
    const { host } = await mountLocale('/dashboard', 'de')

    topBarButton(host, 'Hilfe').click()
    await settle()

    expect(dialog()?.textContent).toContain('Hilfe zum Arbeitsbereich')
    expect(text(dialog()?.querySelectorAll('h3') ?? [])).toEqual([
      'Bereich wählen',
      'Gerät finden',
      'Gerät verschieben',
    ])
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('copies the bug report with a German page line and status', async () => {
    const written: string[] = []
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: (value: string) => {
          written.push(value)
          return Promise.resolve()
        },
      },
    })
    try {
      const { host, setLocale } = await mountLocale('/dashboard', 'de')

      topBarButton(host, 'Fehler melden').click()
      await settle()
      expect(dialog()?.textContent).toContain('Fehler melden')
      expect(dialog()?.textContent).toContain('Zusammenfassung')
      expect(dialog()?.textContent).toContain('Was ist passiert?')
      const summary =
        document.body.querySelector<HTMLInputElement>('#bug-summary')
      const description =
        document.body.querySelector<HTMLTextAreaElement>('#bug-description')
      if (!summary || !description) throw new Error('Missing bug report fields')
      summary.value = 'Tabelle bleibt leer'
      summary.dispatchEvent(new Event('input', { bubbles: true }))
      description.value = 'Die Geräteliste zeigt keine Zeilen.'
      description.dispatchEvent(new Event('input', { bubbles: true }))
      await settle()
      expect(dialog()?.textContent).toContain(
        `Seite: ${window.location.pathname}`,
      )

      document.body
        .querySelector('form')
        ?.dispatchEvent(
          new Event('submit', { bubbles: true, cancelable: true }),
        )
      await settle()

      expect(written).toEqual([
        `Tabelle bleibt leer\n\nSeite: ${window.location.pathname}\n\nDie Geräteliste zeigt keine Zeilen.`,
      ])
      const status = () =>
        dialog()?.querySelector('[role="status"]')?.textContent
      expect(status()).toBe('Bericht kopiert. An das Support-Team weitergeben.')

      await setLocale('en')
      expect(status()).toBe('Report copied. Share it with your support team.')
      expect(i18nWarnings(warn.mock.calls)).toEqual([])
    } finally {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('shows the report text when copying fails', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error('denied')) },
    })
    try {
      const { host } = await mountLocale('/dashboard', 'de')

      topBarButton(host, 'Fehler melden').click()
      await settle()
      document.body
        .querySelector('form')
        ?.dispatchEvent(
          new Event('submit', { bubbles: true, cancelable: true }),
        )
      await settle()

      expect(dialog()?.querySelector('[role="status"]')?.textContent).toBe(
        'Kopieren war nicht möglich. Den Bericht unten auswählen und kopieren.',
      )
      expect(dialog()?.textContent).toContain('Berichtstext')
      expect(
        document.body.querySelector<HTMLTextAreaElement>('#bug-report-copy')
          ?.value,
      ).toContain('Seite:')
    } finally {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })
})

const NBSP = String.fromCodePoint(0xa0)
const germanAgo = new Intl.RelativeTimeFormat('de', {
  numeric: 'auto',
  style: 'short',
})
const englishAgo = new Intl.RelativeTimeFormat('en', {
  numeric: 'auto',
  style: 'short',
})

function cologneAp() {
  const device = devices.find((item) => item.name === 'cologne-ap-02')
  if (!device) throw new Error('The fixture has no cologne-ap-02')
  expect(device.lastSeenMinutes).toBe(38)
  return device
}

const severityNames = (host: HTMLElement) =>
  text(host.querySelectorAll('.dashboard ol li small span.text-sm'))

function resultCount(host: HTMLElement) {
  return host
    .querySelector('#inventory-title')
    ?.closest('section')
    ?.querySelector('.ml-auto')
    ?.textContent?.trim()
}

async function statusOptions(host: HTMLElement, label: string) {
  const trigger = host.querySelector<HTMLButtonElement>(
    `[aria-label="${label}"]`,
  )
  if (!trigger) throw new Error(`Missing status filter ${label}`)
  trigger.dispatchEvent(
    new PointerEvent('pointerdown', {
      bubbles: true,
      cancelable: true,
      button: 0,
    }),
  )
  trigger.dispatchEvent(
    new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }),
  )
  await settle()
  const options = text(document.querySelectorAll('[role="option"]'))
  window.dispatchEvent(
    new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
  )
  await settle()
  return options
}

describe('dashboard in German', () => {
  it('names the heading line, the cards, and the attention reasons', async () => {
    const { host, registry } = await mountLocale('/dashboard', 'de')

    const live = devices.filter((device) => device.health !== 'Offline')
    const total = live.reduce((sum, device) => sum + device.throughput, 0)
    const line = host.querySelector('.page-heading p')?.textContent ?? ''
    expect(line).toContain('16 Geräte')
    expect(line).toContain(
      `${new Intl.NumberFormat('de').format(total)}${NBSP}Mbit/s`,
    )
    expect(line).toContain('Stand ')
    expect(text(host.querySelectorAll('.dashboard h2')).slice(0, 2)).toEqual([
      'KI-Zusammenfassung',
      'Handlungsbedarf',
    ])
    const reason = host.querySelector('.dashboard ul li')
    expect(reason?.textContent).toContain(cologneAp().name)
    expect(reason?.textContent).toContain(
      `zuletzt geantwortet ${germanAgo.format(-38, 'minute')}`,
    )
    expect(registry.view('a:dashboard:view:all')?.target.label).toBe(
      'Dashboard · alle Standorte',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a locale switch in the severity names', async () => {
    const { host, setLocale } = await mountLocale('/dashboard', 'de')
    expect(severityNames(host).length).toBeGreaterThan(0)
    expect(
      severityNames(host).every((name) =>
        ['Kritisch', 'Warnung', 'Info'].includes(name ?? ''),
      ),
    ).toBe(true)

    await setLocale('en')
    expect(
      severityNames(host).every((name) =>
        ['Critical', 'Warning', 'Info'].includes(name ?? ''),
      ),
    ).toBe(true)

    await setLocale('de')
    expect(severityNames(host)).toContain('Warnung')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

describe('inventory in German', () => {
  it('formats counts, ages, and traffic and marks identifiers', async () => {
    const { host, router } = await mountLocale('/devices', 'de')
    const offline = cologneAp()

    expect(host.querySelector('#inventory-title')?.textContent).toContain(
      'Geräteinventar',
    )
    expect(resultCount(host)).toBe('16 Ergebnisse')
    expect(host.querySelector('tbody tr .seen')?.textContent?.trim()).toBe(
      germanAgo.format(-38, 'minute'),
    )
    const live = devices.find((device) => device.health === 'Healthy')
    if (!live) throw new Error('The fixture has no healthy device')
    expect(
      host.querySelector(`tbody tr[data-device-id="${live.id}"] .traffic`)
        ?.textContent,
    ).toContain(
      `${new Intl.NumberFormat('de').format(live.throughput)}${NBSP}Mbit/s`,
    )
    const row = host.querySelector(`tbody tr[data-device-id="${offline.id}"]`)
    expect(row?.querySelector('strong')?.closest('[translate="no"]')).not.toBe(
      null,
    )
    const address = row?.querySelector('td.font-mono')
    expect(address?.textContent?.trim()).toBe(offline.address)
    expect(address?.closest('[translate="no"]')).not.toBe(null)
    expect(await statusOptions(host, 'Nach Status filtern')).toEqual([
      'Alle Status',
      `Handlungsbedarf (${devices.filter((item) => item.health !== 'Healthy').length})`,
      'Gesund',
      'Beeinträchtigt',
      'Offline',
    ])

    await router.push({ path: '/devices', query: { search: offline.name } })
    await settle()

    expect(resultCount(host)).toBe('1 Ergebnis')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a locale switch', async () => {
    const { host, setLocale } = await mountLocale('/devices', 'de')
    expect(host.querySelector('h1')?.textContent?.trim()).toBe('Geräte')
    expect(resultCount(host)).toBe('16 Ergebnisse')

    await setLocale('en')

    expect(host.querySelector('h1')?.textContent?.trim()).toBe('Devices')
    expect(host.querySelector('#inventory-title')?.textContent).toContain(
      'Device inventory',
    )
    expect(resultCount(host)).toBe('16 results')
    expect(host.querySelector('tbody tr .seen')?.textContent?.trim()).toBe(
      englishAgo.format(-38, 'minute'),
    )
    expect(await statusOptions(host, 'Filter by status')).toContain(
      'All statuses',
    )

    await setLocale('de')

    expect(host.querySelector('h1')?.textContent?.trim()).toBe('Geräte')
    expect(resultCount(host)).toBe('16 Ergebnisse')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

describe('language switch in the top bar', () => {
  it('turns the navigation and heading German with no remount', async () => {
    const { host } = await mountLocale('/devices', 'en')
    const inventory = host.querySelector('#inventory-title')
    const heading = host.querySelector('h1')
    const button = host.querySelector<HTMLButtonElement>(
      '.topbar-tools button.locale-switcher',
    )
    if (!button) throw new Error('Missing language switch')
    expect(heading?.textContent?.trim()).toBe('Devices')
    expect(inventory?.textContent).toContain('Device inventory')

    button.click()
    await settle()

    expect(host.querySelector('#inventory-title')).toBe(inventory)
    expect(host.querySelector('h1')).toBe(heading)
    expect(heading?.textContent?.trim()).toBe('Geräte')
    expect(inventory?.textContent).toContain('Geräteinventar')
    expect(text(host.querySelectorAll('.nav-text'))).toEqual([
      'Dashboard',
      'Geräte',
      'Topologie',
      'Clients',
      'Standorte',
    ])
    expect(button.textContent).toContain('Sprache auf English umstellen')
    expect(localStorage.getItem('flowseer.locale')).toBe('de')

    button.click()
    await settle()

    expect(heading?.textContent?.trim()).toBe('Devices')
    expect(inventory?.textContent).toContain('Device inventory')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

describe('sites in German', () => {
  it('names the heading, the count, and the columns', async () => {
    const { host } = await mountLocale('/sites', 'de')

    const heading = host.querySelector('#sites-title')
    expect(heading?.textContent).toContain('Standorte')
    expect(heading?.querySelector('span')?.textContent?.trim()).toBe('4')
    const table = heading?.closest('section')
    expect(text(table?.querySelectorAll('th') ?? [])).toEqual([
      'Standort',
      'Zustand',
      'Offenes Problem',
      'Geräte',
      'Geräte an diesem Standort',
    ])
    expect(table?.textContent).toContain('1 offline')
    expect(table?.textContent).toContain(germanAgo.format(-38, 'minute'))
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

const clientsSection = (host: HTMLElement) =>
  host.querySelector('#clients-title')?.closest('section')

function clientCount(host: HTMLElement) {
  return clientsSection(host)?.querySelector('.ml-auto')?.textContent?.trim()
}

describe('clients in German', () => {
  it('names the columns, the bands, the readings, and the counts', async () => {
    const { host, router, registry } = await mountLocale('/clients', 'de')
    const all = clientsOf(devices)
    const first = all[0]
    if (!first) throw new Error('The fixture has no clients')

    const section = clientsSection(host)
    expect(section?.querySelector('#clients-title')?.textContent).toContain(
      'Verbundene Clients',
    )
    expect(text(section?.querySelectorAll('th') ?? [])).toEqual([
      'Client',
      'MAC-Adresse',
      'Zugangspunkt',
      'Band',
      'Signal',
      'Datenverkehr',
    ])
    expect(section?.querySelector('input')?.getAttribute('placeholder')).toBe(
      'Nach Hostname, IP- oder MAC-Adresse suchen…',
    )
    expect(await statusOptions(host, 'Nach Band filtern')).toEqual([
      'Alle Bänder',
      '2,4 GHz',
      '5 GHz',
      '6 GHz',
    ])
    expect(clientCount(host)).toBe(
      `${new Intl.NumberFormat('de').format(all.length)} Ergebnisse`,
    )

    const row = section?.querySelector('tbody tr')
    const quality = {
      Strong: 'Stark',
      Fair: 'Mäßig',
      Weak: 'Schwach',
    }[signalQuality(first.signal)]
    const reading = `${first.signal}${NBSP}dBm`
    expect(row?.querySelector('td:nth-child(5)')?.textContent).toContain(
      quality,
    )
    expect(row?.querySelector('td:nth-child(5)')?.textContent).toContain(
      reading,
    )
    expect(row?.querySelector('td:nth-child(6)')?.textContent).toContain(
      `${first.throughput}${NBSP}Mbit/s`,
    )
    const mac = row?.querySelector('td.font-mono')
    expect(mac?.textContent?.trim()).toBe(first.mac)
    expect(mac?.closest('[translate="no"]')).not.toBe(null)
    expect(row?.querySelector('strong')?.closest('[translate="no"]')).not.toBe(
      null,
    )
    const target = registry
      .list()
      .find((item) => item.id === `a:clients:client:${first.id}`)
    expect(target?.context.signal).toBe(`${reading} (${quality})`)
    expect(registry.view('a:clients:view:all')?.target.label).toBe(
      'Verbundene Clients',
    )

    await router.push({ path: '/clients', query: { q: first.mac } })
    await settle()

    expect(clientCount(host)).toBe('1 Ergebnis')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('names the empty state and the access point filter', async () => {
    const { host } = await mountLocale(
      '/clients?ap=dev-3&q=nothing-matches-this',
      'de',
    )

    expect(clientsSection(host)?.textContent).toContain(
      'Keine Clients passen zu dieser Ansicht',
    )
    expect(clientsSection(host)?.textContent).toContain(
      'Eine andere Suche oder ein anderes Band versuchen.',
    )
    const stop = clientsSection(host)?.querySelector(
      'button[aria-label^="Filter nach"]',
    )
    expect(stop?.getAttribute('aria-label')).toBe(
      'Filter nach berlin-ap-01 aufheben',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a locale switch in the band options', async () => {
    const { host, setLocale } = await mountLocale('/clients', 'de')
    expect(await statusOptions(host, 'Nach Band filtern')).toContain('2,4 GHz')

    await setLocale('en')

    expect(await statusOptions(host, 'Filter by band')).toEqual([
      'All bands',
      '2.4 GHz',
      '5 GHz',
      '6 GHz',
    ])
    expect(clientCount(host)).toMatch(/ results$/)

    await setLocale('de')

    expect(await statusOptions(host, 'Nach Band filtern')).toContain('2,4 GHz')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

const deviceSeverityNames = (host: HTMLElement) =>
  text(
    host
      .querySelector('#issues-title')
      ?.closest('section')
      ?.querySelectorAll('li small span.text-sm') ?? [],
  )

describe('device route in German', () => {
  it('names the status, the identifiers, and the paths', async () => {
    const offline = cologneAp()
    const { host } = await mountLocale(`/devices/${offline.id}`, 'de')

    expect(host.querySelector('#issues-title')?.textContent?.trim()).toBe(
      'Warum Handlungsbedarf besteht',
    )
    expect(host.querySelector('h1')?.closest('[translate="no"]')).not.toBe(null)
    expect(host.textContent).toContain(
      `Zuletzt geantwortet ${germanAgo.format(-38, 'minute')}`,
    )
    expect(host.textContent).toContain('Nicht erreichbar')
    expect(host.textContent).toContain(
      'Für Nord Retail gibt es keinen anderen Standort, an den dieses Gerät verschoben werden könnte.',
    )
    expect(host.textContent).toContain('Eskalationszusammenfassung kopieren')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('names the move control and the downlinks', async () => {
    const { host } = await mountLocale('/devices/dev-2', 'de')

    expect(host.querySelector('summary')?.textContent?.trim()).toBe(
      'Zu einem anderen Standort verschieben',
    )
    expect(host.textContent).toContain('Gerät verschieben')
    expect(host.querySelector('#links-title')?.textContent).toContain(
      'Downlinks',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('shows a translated notice when moving a device fails and updates it on locale switch', async () => {
    vi.spyOn(fleetDomain, 'moveDevice').mockImplementation(() => {
      throw new Error('Choose a site owned by the same tenant.')
    })
    const { host, setLocale } = await mountLocale('/devices/dev-2', 'de')
    const form = host.querySelector('form')
    form?.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }),
    )
    await settle()

    const notice = host.querySelector('main [role="status"]')
    expect(notice?.textContent).toContain(
      'Der Standort konnte nicht zugewiesen werden.',
    )
    expect(notice?.textContent).not.toContain(
      'Choose a site owned by the same tenant.',
    )

    await setLocale('en')
    expect(notice?.textContent).toContain('Could not assign site.')

    await setLocale('de')
    expect(notice?.textContent).toContain(
      'Der Standort konnte nicht zugewiesen werden.',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a locale switch in the severity names and the age', async () => {
    const offline = cologneAp()
    const { host, setLocale } = await mountLocale(
      `/devices/${offline.id}`,
      'de',
    )
    expect(deviceSeverityNames(host)).toEqual(['Kritisch'])

    await setLocale('en')

    expect(deviceSeverityNames(host)).toEqual(['Critical'])
    expect(host.querySelector('#issues-title')?.textContent?.trim()).toBe(
      'Why it needs attention',
    )
    expect(host.textContent).toContain(
      `Last answered ${englishAgo.format(-38, 'minute')}`,
    )

    await setLocale('de')

    expect(deviceSeverityNames(host)).toEqual(['Kritisch'])
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

const portsFixture: Port[] = [
  {
    name: 'xe-0/1/0',
    status: 'Up',
    speed: 10000,
    neighborId: 'dev-1',
    throughput: 1234.5,
  },
  {
    name: 'ge-0/0/0',
    status: 'Up',
    speed: 2500,
    endpoint: 'Printer',
    throughput: 3,
    poe: 15,
  },
  { name: 'ge-0/0/1', status: 'Disabled', throughput: 0 },
]

async function mountPorts(locale: WebLocale, ports: Port[]) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n(locale)
  const app = createApp(DevicePorts, {
    ports,
    device: (id: string) => devices.find((item) => item.id === id),
  })
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()
  await nextTick()
  return {
    host,
    async setLocale(next: WebLocale) {
      i18n.global.locale.value = next
      await nextTick()
    },
  }
}

describe('device ports in German', () => {
  it('counts the ports and formats speeds, rates, and power', async () => {
    const { host } = await mountPorts('de', portsFixture)

    expect(host.querySelector('ol')?.getAttribute('aria-label')).toBe('3 Ports')
    expect(host.querySelector('.port-summary')?.textContent?.trim()).toBe(
      `2 von 3 verbunden · 15${NBSP}W PoE`,
    )
    expect(text(host.querySelectorAll('.port-rate'))).toEqual([
      `10G · 1.234,5${NBSP}Mbit/s`,
      `2,5G · 3${NBSP}Mbit/s`,
    ])
    expect(labelsOf(host.querySelectorAll('ol button'))).toEqual([
      'xe-0/1/0 · Verbunden · 10G · berlin-gw-01',
      'ge-0/0/0 · Verbunden · 2,5G · Printer',
      'ge-0/0/1 · Deaktiviert',
    ])
    for (const name of host.querySelectorAll('.port-name')) {
      expect(name.closest('[translate="no"]')).not.toBe(null)
    }
    expect(
      host.querySelector('.port-neighbor')?.closest('[translate="no"]'),
    ).not.toBe(null)
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('selects the singular form and omits power without PoE', async () => {
    const [first] = portsFixture
    if (!first) throw new Error('Missing port fixture')
    const { host } = await mountPorts('de', [first])

    expect(host.querySelector('ol')?.getAttribute('aria-label')).toBe('1 Port')
    expect(host.querySelector('.port-summary')?.textContent?.trim()).toBe(
      '1 von 1 verbunden',
    )
  })

  it('follows a locale switch', async () => {
    const { host, setLocale } = await mountPorts('de', portsFixture)

    await setLocale('en')

    expect(host.querySelector('ol')?.getAttribute('aria-label')).toBe('3 ports')
    expect(host.querySelector('.port-summary')?.textContent?.trim()).toBe(
      `2 of 3 up · 15${NBSP}W PoE`,
    )
    expect(text(host.querySelectorAll('.port-rate'))).toEqual([
      `10G · 1,234.5${NBSP}Mbit/s`,
      `2.5G · 3${NBSP}Mbit/s`,
    ])
    expect(labelsOf(host.querySelectorAll('ol button'))[2]).toBe(
      'ge-0/0/1 · Disabled',
    )

    await setLocale('de')

    expect(host.querySelector('ol')?.getAttribute('aria-label')).toBe('3 Ports')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

interface SweepRoute {
  name: string
  path: () => string
  heading: string
  words: Record<WebLocale, string>
}

const sweepRoutes: SweepRoute[] = [
  {
    name: '/dashboard',
    path: () => '/dashboard',
    heading: '#summary-title',
    words: { en: 'AI summary', de: 'KI-Zusammenfassung' },
  },
  {
    name: '/devices',
    path: () => '/devices',
    heading: '#inventory-title',
    words: { en: 'Device inventory', de: 'Geräteinventar' },
  },
  {
    name: 'a device route',
    path: () => `/devices/${cologneAp().id}`,
    heading: '#issues-title',
    words: {
      en: 'Why it needs attention',
      de: 'Warum Handlungsbedarf besteht',
    },
  },
  {
    name: '/clients',
    path: () => '/clients',
    heading: '#clients-title',
    words: { en: 'Connected clients', de: 'Verbundene Clients' },
  },
  {
    name: '/sites',
    path: () => '/sites',
    heading: '#sites-title',
    words: { en: 'Sites', de: 'Standorte' },
  },
]

const headingText = (host: HTMLElement, selector: string) =>
  host.querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim() ?? ''

// Every `view.*` message whose English text differs from its German text and
// holds no placeholder or plural bar. Such a string alone in an element or an
// attribute means the German view kept an English message.
function englishOnlyTexts() {
  const found = new Set<string>()
  const walk = (en: unknown, de: unknown) => {
    if (typeof en === 'string' && typeof de === 'string') {
      if (en !== de && !/[{|]/.test(en)) found.add(en)
      return
    }
    if (en === null || typeof en !== 'object') return
    for (const [key, value] of Object.entries(en)) {
      walk(value, (de as Record<string, unknown> | null)?.[key])
    }
  }
  walk(enCatalog.view, deCatalog.view)
  return found
}

function visibleTexts(host: HTMLElement) {
  const found: string[] = []
  for (const element of host.querySelectorAll('*')) {
    if (element.childElementCount === 0 && element.textContent) {
      found.push(element.textContent.replace(/\s+/g, ' ').trim())
    }
    for (const name of ['aria-label', 'title', 'placeholder']) {
      const value = element.getAttribute(name)
      if (value) found.push(value.trim())
    }
  }
  return found
}

describe.each(sweepRoutes)('locale sweep of $name', (route) => {
  it('reads German with no English message left', async () => {
    const { host } = await mountLocale(route.path(), 'de')

    expect(headingText(host, route.heading)).toContain(route.words.de)
    const english = englishOnlyTexts()
    expect(visibleTexts(host).filter((item) => english.has(item))).toEqual([])
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('reads English', async () => {
    const { host } = await mountLocale(route.path(), 'en')

    expect(headingText(host, route.heading)).toContain(route.words.en)
    expect(host.querySelector('.nav-label')?.textContent?.trim()).toBe(
      'WORKSPACE',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a live switch from English to German and back', async () => {
    const { host, setLocale } = await mountLocale(route.path(), 'en')
    expect(headingText(host, route.heading)).toContain(route.words.en)

    await setLocale('de')
    expect(headingText(host, route.heading)).toContain(route.words.de)
    expect(host.querySelector('.nav-label')?.textContent?.trim()).toBe(
      'ARBEITSBEREICH',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])

    await setLocale('en')
    expect(headingText(host, route.heading)).toContain(route.words.en)
    expect(host.querySelector('.nav-label')?.textContent?.trim()).toBe(
      'WORKSPACE',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a live switch from German to English and back', async () => {
    const { host, setLocale } = await mountLocale(route.path(), 'de')
    expect(headingText(host, route.heading)).toContain(route.words.de)

    await setLocale('en')
    expect(headingText(host, route.heading)).toContain(route.words.en)
    expect(i18nWarnings(warn.mock.calls)).toEqual([])

    await setLocale('de')
    expect(headingText(host, route.heading)).toContain(route.words.de)
    expect(
      visibleTexts(host).filter((item) => englishOnlyTexts().has(item)),
    ).toEqual([])
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})
