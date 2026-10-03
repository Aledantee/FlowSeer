// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, createApp, h, nextTick, ref } from 'vue'
import DeviceView from './DeviceView.vue'
import { pageContext, pageFor } from './navigation/page'
import { workspaceContext, type NoticeKey } from './navigation/workspace'
import { devices, moveDevice, sites, tenants } from './domain/fleet'
import type { Device } from './domain/fleet'
import { createAiRegistry, createAiTargetDirective } from './ai'
import type { AiRegistry } from './ai'
import { aiRegistryKey } from './ui/ai/context'
import { createWebI18n } from './i18n'
import type { WebLocale } from './i18n'
import { i18nWarnings, unmarkedIdentifiers } from './i18n/testing'
import { fixtureIdentifiers } from './domain/testing'

const identifiers = fixtureIdentifiers()

let dispose = () => {}
let registry: AiRegistry
let warn: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.useRealTimers()
  vi.restoreAllMocks()
  Reflect.deleteProperty(navigator, 'clipboard')
})

async function mountDeviceView(
  device: Device,
  fleet: Device[],
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const loc = computed(() => ({ path: `/devices/${device.id}`, query: {} }))
  const page = pageFor(
    loc,
    () => true,
    async () => {},
  )
  const workspace = {
    fleet: ref(fleet),
    message: ref<NoticeKey | ''>(''),
    reassign: () => {},
    move: ref(undefined),
    undoMove: () => {},
    dismissNotice: () => {},
    siteName: (id: string) => sites.find((s) => s.id === id)?.name ?? '',
    tenantName: (siteId: string) =>
      tenants.find((t) => t.id === sites.find((s) => s.id === siteId)?.tenantId)
        ?.name ?? '',
    follow: async () => {},
    activePane: ref('main' as const),
    peek: ref([]),
    sideDeviceId: computed(() => undefined),
  }
  const app = createApp({
    setup() {
      return () =>
        h(DeviceView, {
          device,
          fleet,
          allowedSites: sites,
          siteName: workspace.siteName,
          tenantName: workspace.tenantName,
        })
    },
  })
  registry = createAiRegistry()
  app.use(createWebI18n(locale))
  app.provide(pageContext, page)
  app.provide(workspaceContext, workspace)
  app.provide(aiRegistryKey, registry)
  app.directive('ai-target', createAiTargetDirective(registry))
  app.mount(host)
  dispose = () => app.unmount()
  await nextTick()
  return host
}

describe('DeviceView uplink', () => {
  it('renders site edge when an access point is reassigned across sites', async () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!ap) throw new Error('Missing fixture')
    const reassigned = moveDevice(ap, 'hamburg')
    const fleet = devices.map((item) =>
      item.id === reassigned.id ? reassigned : item,
    )
    const host = await mountDeviceView(reassigned, fleet)
    expect(host.textContent).toContain('Site edge')
    expect(host.textContent).not.toContain('berlin-sw-01')
  })

  it('renders active uplink when within the same site', async () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!ap) throw new Error('Missing fixture')
    const host = await mountDeviceView(ap, devices)
    expect(host.textContent).toContain('berlin-sw-01')
  })
})

describe('DeviceView status typography', () => {
  it('renders named event severity at the status text size', async () => {
    const offline = devices.find((device) => device.health === 'Offline')
    if (!offline) throw new Error('Missing offline fixture')

    const host = await mountDeviceView(offline, devices)
    const severity = [...host.querySelectorAll('span')].find((item) =>
      ['Critical', 'Warning'].includes(item.textContent?.trim() ?? ''),
    )

    expect(severity?.classList).toContain('text-sm')
  })
})

describe('DeviceView AI targets', () => {
  it('registers a standalone view root and client rows with context', async () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!ap) throw new Error('Missing fixture')
    await mountDeviceView(ap, devices)

    const root = registry.view(`standalone:device:view:${ap.id}`)
    expect(root?.target.context).toMatchObject({
      health: ap.health,
      site: 'Berlin Mitte',
    })

    const clients = registry
      .list()
      .filter((item) => item.id.startsWith('standalone:device:client:'))
    expect(clients.length).toBeGreaterThan(0)
    expect(clients[0]?.context.accessPoint).toBe('berlin-ap-01')
  })

  it('registers a downlink row with its uplink context', async () => {
    const sw = devices.find((device) => device.name === 'berlin-sw-01')
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!sw || !ap) throw new Error('Missing fixture')
    await mountDeviceView(sw, devices)

    const downlink = registry.view(`standalone:device:downlink:${ap.id}`)
    expect(downlink?.target.kind).toBe('downlink')
    expect(downlink?.target.label).toBe('berlin-ap-01')
    expect(downlink?.target.context).toMatchObject({
      name: 'berlin-ap-01',
      uplink: 'berlin-sw-01',
    })
  })
})

const germanAgo = new Intl.RelativeTimeFormat('de', {
  numeric: 'auto',
  style: 'short',
})

function offlineDevice() {
  const offline = devices.find((device) => device.health === 'Offline')
  if (!offline) throw new Error('Missing offline fixture')
  return offline
}

// Faking only the timeout keeps the microtask flush that Vue renders on.
function fakePollTimer() {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
}

function buttonWithText(host: HTMLElement, label: string) {
  const button = [...host.querySelectorAll('button')].find(
    (item) => item.textContent?.trim() === label,
  )
  if (!button) throw new Error(`Missing button ${label}`)
  return button
}

describe('DeviceView in German', () => {
  it('names the status, the paths, the lifecycle, and the move control', async () => {
    const offline = offlineDevice()
    const host = await mountDeviceView(offline, devices, 'de')

    expect(host.querySelector('#issues-title')?.textContent?.trim()).toBe(
      'Warum Handlungsbedarf besteht',
    )
    expect(host.querySelector('#paths-title')?.textContent?.trim()).toBe(
      'Wie FlowSeer es erreicht',
    )
    expect(host.textContent).toContain(
      `Zuletzt geantwortet ${germanAgo.format(-38, 'minute')}`,
    )
    const terms = [...host.querySelectorAll('dt')].map((item) =>
      item.textContent?.trim(),
    )
    expect(terms).toEqual(['Lebenszyklus', 'Clients', 'Datenverkehr', 'Uplink'])
    const definitions = [...host.querySelectorAll('dd')].map((item) =>
      item.textContent?.trim(),
    )
    expect(definitions).toEqual(['Aktiv', '—', '—', 'cologne-sw-01'])
    const reachability = [
      ...(host
        .querySelector('#paths-title')
        ?.closest('section')
        ?.querySelectorAll('li span.text-right strong') ?? []),
    ].map((item) => item.textContent?.trim())
    expect(reachability).toContain('Nicht erreichbar')
    expect(host.querySelector('summary')?.textContent?.trim()).toBe(
      'Zu einem anderen Standort verschieben',
    )
    expect(host.textContent).not.toContain('Poll now')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('names the site edge when the uplink is at another site', async () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!ap) throw new Error('Missing fixture')
    const reassigned = moveDevice(ap, 'hamburg')
    const fleet = devices.map((item) =>
      item.id === reassigned.id ? reassigned : item,
    )
    const host = await mountDeviceView(reassigned, fleet, 'de')

    const uplink = [...host.querySelectorAll('dt')].find(
      (item) => item.textContent?.trim() === 'Uplink',
    )
    expect(uplink?.nextElementSibling?.textContent?.trim()).toBe('Standortrand')
  })

  it('writes the escalation summary in German', async () => {
    const offline = offlineDevice()
    const written: string[] = []
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: (text: string) => {
          written.push(text)
          return Promise.resolve()
        },
      },
    })
    const host = await mountDeviceView(offline, devices, 'de')

    buttonWithText(host, 'Eskalationszusammenfassung kopieren').click()
    await nextTick()
    await nextTick()

    const lines = written[0]?.split('\n') ?? []
    expect(lines[0]).toBe(
      `${offline.name} (${offline.kind}, ${offline.address}) ist offline.`,
    )
    expect(lines[1]).toBe('Standort: Cologne Central, Nord Retail.')
    expect(lines[2]).toBe(
      `Zuletzt geantwortet ${germanAgo.format(-38, 'minute')}`,
    )
    expect(lines[3]).toBe('Beide Pfade sind nicht erreichbar.')
    expect(lines[4]).toBe(
      `- Cologne Central edge: nicht erreichbar, geprüft ${germanAgo.format(-1, 'minute')}`,
    )
    expect(lines.at(-1)).toBe('3 von 3 weiteren Geräten am Standort antworten.')
    expect(host.textContent).toContain(
      'Zusammenfassung kopiert. In das Ticket oder die Nachricht einfügen.',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('says why the copy failed and shows the summary', async () => {
    const offline = offlineDevice()
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error('denied')) },
    })
    const host = await mountDeviceView(offline, devices, 'de')

    buttonWithText(host, 'Eskalationszusammenfassung kopieren').click()
    await nextTick()
    await nextTick()

    expect(host.textContent).toContain(
      'Kopieren war nicht möglich. Die Zusammenfassung unten auswählen.',
    )
    expect(host.querySelector('pre')?.textContent).toContain(
      `${offline.name} (${offline.kind}, ${offline.address}) ist offline.`,
    )
    expect(unmarkedIdentifiers(host, identifiers)).toEqual([])
  })

  it('words the neighbour sentence and the poll result', async () => {
    fakePollTimer()
    const offline = offlineDevice()
    const host = await mountDeviceView(offline, devices, 'de')

    expect(host.querySelector('.bg-subtle p')?.textContent?.trim()).toBe(
      'Die anderen 3 Geräte bei Cologne Central antworten, daher liegt der Fehler wahrscheinlich an diesem Gerät oder seiner Verbindung.',
    )
    expect(
      host.querySelector('.bg-subtle p span[translate="no"]')?.textContent,
    ).toBe('Cologne Central')

    buttonWithText(host, 'Jetzt abfragen').click()
    await nextTick()
    expect(buttonWithText(host, 'Abfrage läuft…').disabled).toBe(true)
    await vi.advanceTimersByTimeAsync(1200)

    expect(host.querySelector('[role="status"]')?.textContent?.trim()).toBe(
      `Weiterhin keine Antwort. Beide Pfade sind nicht erreichbar. (Letzte Antwort ${germanAgo.format(-38, 'minute')})`,
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('words an answered poll', async () => {
    fakePollTimer()
    const offline = offlineDevice()
    const partial = {
      ...offline,
      bindings: offline.bindings.map((binding, index) =>
        index ? { ...binding, reachability: 'Reachable' as const } : binding,
      ),
    }
    const fleet = devices.map((item) =>
      item.id === offline.id ? partial : item,
    )
    const host = await mountDeviceView(partial, fleet, 'de')

    buttonWithText(host, 'Jetzt abfragen').click()
    await nextTick()
    await vi.advanceTimersByTimeAsync(1200)

    expect(host.querySelector('[role="status"]')?.textContent?.trim()).toBe(
      'Gerade eben geantwortet. Weiterhin offline.',
    )
  })

  it('reads the device header and client rows with identifiers marked', async () => {
    const ap = devices.find((device) => device.name === 'berlin-ap-01')
    if (!ap) throw new Error('Missing fixture')
    const host = await mountDeviceView(ap, devices, 'de')

    expect(host.querySelector('#clients-title')?.textContent).toContain(
      'Verbundene Clients',
    )
    expect(host.querySelector('#links-title')?.textContent).toContain(
      'Downlinks',
    )
    const header = host.querySelector('header p')
    expect(header?.textContent?.replace(/\s+/g, ' ').trim()).toBe(
      `${ap.kind} · ${ap.address} · Berlin Mitte · Aurora Germany`,
    )
    expect(header?.querySelector('span[translate="no"]')?.textContent).toBe(
      ap.address,
    )
    const row = host
      .querySelector('#clients-title')
      ?.closest('section')
      ?.querySelector('li')
    expect(row?.querySelector('strong')?.closest('[translate="no"]')).not.toBe(
      null,
    )
    expect(row?.querySelector('small')?.closest('[translate="no"]')).not.toBe(
      null,
    )
    expect(row?.querySelector('span.text-2xs')?.textContent?.trim()).toMatch(
      /^(2,4|5|6) GHz · (Stark|Mäßig|Schwach)$/,
    )
  })
})
