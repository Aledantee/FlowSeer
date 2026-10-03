// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { TooltipProvider } from 'reka-ui'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from '../FleetView.vue'
import { UiAppRoot } from '../ui'
import { createAiRegistry, createAiTargetDirective } from '../ai'
import { aiRegistryKey } from '../ui/ai/context'
import { DOCK_KEY } from '../navigation/dock'
import type { Device } from '../domain/fleet'
import { devices } from '../domain/fleet'
import * as clientsModule from '../domain/clients'
import { clientsOf } from '../domain/clients'
import { portsOf } from '../domain/telemetry'
import type { SearchResult } from '../domain/search'
import { SHORTCUTS, isMac, keysOf } from '../navigation/shortcuts'
import GlobalSearch, { type SearchPage } from './GlobalSearch.vue'
import { createWebI18n } from '../i18n'
import type { WebLocale } from '../i18n'
import { fixtureIdentifiers } from '../domain/testing'
import { i18nWarnings, unmarkedIdentifiers } from '../i18n/testing'
import { rememberRecent } from './recentSearches'

const pages: SearchPage[] = [
  {
    id: 'overview',
    title: 'Page One',
    parts: [{ text: 'First page' }],
    icon: 'dashboard',
  },
  {
    id: 'inventory',
    title: 'Page Two',
    parts: [{ text: 'Second page' }],
    icon: 'devices',
  },
]

let dispose = () => {}
let warn: ReturnType<typeof vi.spyOn>

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
})

afterEach(() => {
  dispose()
  dispose = () => {}
  localStorage.clear()
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
}

interface SearchHandlers {
  onSelect?: (result: SearchResult, beside: boolean) => void
  onDock?: (result: SearchResult) => void
}

interface SearchOptions extends SearchHandlers {
  locale?: WebLocale
  fleet?: Device[]
  pages?: SearchPage[]
}

async function mountSearchClosed(options: SearchOptions = {}) {
  const {
    locale,
    fleet = [],
    pages: customPages = pages,
    ...handlers
  } = options
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n(locale)
  const app = createApp({
    render: () =>
      h(TooltipProvider, null, () =>
        h(GlobalSearch, {
          fleet,
          pages: customPages,
          canSplit: true,
          ...handlers,
        }),
      ),
  })
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()

  await settle()
  return {
    host,
    async setLocale(next: WebLocale) {
      i18n.global.locale.value = next
      await settle()
    },
  }
}

async function mountSearch(handlers: SearchHandlers) {
  const { host } = await mountSearchClosed(handlers)

  host.querySelector<HTMLButtonElement>('.search-trigger')?.click()
  await settle()

  const input =
    document.body.querySelector<HTMLInputElement>('[role="combobox"]')
  expect(input).not.toBeNull()
  if (!input) throw new Error('Global search input did not open.')

  input.value = 'Page'
  input.dispatchEvent(new InputEvent('input', { bubbles: true }))
  await settle()

  const options = document.body.querySelectorAll<HTMLElement>('[role="option"]')
  expect(options).toHaveLength(2)
  return { input, options }
}

function keydown(input: HTMLInputElement, init: KeyboardEventInit) {
  input.dispatchEvent(
    new KeyboardEvent('keydown', {
      key: 'Enter',
      code: 'Enter',
      bubbles: true,
      cancelable: true,
      ...init,
    }),
  )
}

describe('global search selection', () => {
  it('opens the highlighted result once with ordinary Enter', async () => {
    const selected: [SearchResult, boolean][] = []
    const { input } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    keydown(input, {})
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'overview' }), false],
    ])
  })

  it('opens a clicked result normally', async () => {
    const selected: [SearchResult, boolean][] = []
    const { options } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    options[1]?.click()
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'inventory' }), false],
    ])
  })

  it('opens the non-first highlighted result once with Shift+Enter', async () => {
    const selected: [SearchResult, boolean][] = []
    const { input } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    keydown(input, { key: 'ArrowDown', code: 'ArrowDown' })
    await settle()
    keydown(input, { shiftKey: true })
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'inventory' }), true],
    ])
  })

  it('sends the highlighted result to the dock once with Alt+Enter', async () => {
    const docked: SearchResult[] = []
    const { input } = await mountSearch({
      onDock: (result) => docked.push(result),
    })

    keydown(input, { altKey: true })
    await settle()

    expect(docked).toEqual([expect.objectContaining({ id: 'overview' })])
  })

  it('preserves modifier-click side-by-side behavior', async () => {
    const selected: [SearchResult, boolean][] = []
    const { options } = await mountSearch({
      onSelect: (result, beside) => selected.push([result, beside]),
    })

    options[1]?.dispatchEvent(
      new MouseEvent('click', {
        bubbles: true,
        cancelable: true,
        metaKey: true,
      }),
    )
    await settle()

    expect(selected).toEqual([
      [expect.objectContaining({ id: 'inventory' }), true],
    ])
  })
})

describe('global search shortcuts', () => {
  it('displays registered shortcut keys and opens search with the registered shortcut', async () => {
    const { host } = await mountSearchClosed()
    const trigger = host.querySelector<HTMLButtonElement>('.search-trigger')
    const kbd = trigger?.querySelector('kbd')
    expect(kbd?.textContent?.trim()).toBe(
      keysOf(SHORTCUTS.search).join(isMac() ? '' : ' '),
    )

    const event = new KeyboardEvent('keydown', {
      key: 'k',
      code: SHORTCUTS.search.code,
      bubbles: true,
      cancelable: true,
      ...(isMac() ? { metaKey: true } : { ctrlKey: true }),
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(true)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).not.toBeNull()
  })

  it('does not open search when the platform modifier does not match', async () => {
    await mountSearchClosed()

    const event = new KeyboardEvent('keydown', {
      key: 'k',
      code: SHORTCUTS.search.code,
      bubbles: true,
      cancelable: true,
      ...(isMac() ? { ctrlKey: true } : { metaKey: true }),
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })

  it('does not open search with Cmd/Ctrl+K when a modal is already open', async () => {
    await mountSearchClosed()

    const modal = document.createElement('div')
    modal.setAttribute('role', 'dialog')
    modal.dataset.state = 'open'
    document.body.append(modal)

    const event = new KeyboardEvent('keydown', {
      key: 'k',
      code: 'KeyK',
      bubbles: true,
      cancelable: true,
      ...(isMac() ? { metaKey: true } : { ctrlKey: true }),
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })

  it('does not open search with / when focused in a contenteditable element', async () => {
    await mountSearchClosed()

    const editable = document.createElement('div')
    editable.contentEditable = 'true'
    document.body.append(editable)
    editable.focus()

    const event = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      bubbles: true,
      cancelable: true,
    })
    editable.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })

  it('opens search with / when not typing, but not when a modal is open', async () => {
    await mountSearchClosed()

    const modal = document.createElement('div')
    modal.setAttribute('role', 'dialog')
    modal.dataset.state = 'open'
    document.body.append(modal)

    const blockedEvent = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(blockedEvent)
    await settle()

    expect(blockedEvent.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()

    modal.remove()

    const allowedEvent = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(allowedEvent)
    await settle()

    expect(allowedEvent.defaultPrevented).toBe(true)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).not.toBeNull()
  })

  it('does not open search with / when modified by Shift', async () => {
    await mountSearchClosed()

    const event = new KeyboardEvent('keydown', {
      key: '/',
      code: 'Slash',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(event)
    await settle()

    expect(event.defaultPrevented).toBe(false)
    expect(
      document.body.querySelector<HTMLInputElement>('[role="combobox"]'),
    ).toBeNull()
  })
})

async function openSearch(host: HTMLElement) {
  host.querySelector<HTMLButtonElement>('.search-trigger')?.click()
  await settle()
}

async function typeQuery(text: string) {
  const input =
    document.body.querySelector<HTMLInputElement>(
      '[role="dialog"] [role="combobox"], [role="dialog"] input',
    ) ?? document.body.querySelector<HTMLInputElement>('[role="combobox"]')
  if (!input) throw new Error('Global search input did not open.')
  input.value = text
  input.dispatchEvent(new InputEvent('input', { bubbles: true }))
  await settle()
}

const textOf = (selector: string) =>
  [...document.body.querySelectorAll(selector)].map((element) =>
    element.textContent?.trim(),
  )
const headings = () =>
  textOf('[role="group"] > [id^="reka-combobox-group-label"]')
const recentHeading = () => textOf('[role="group"] > div > span')
const details = () => textOf('.search-result small')

// The first managed port that is up and has a managed device on the far end.
function linkedPort() {
  for (const device of devices) {
    const port = portsOf(devices, device).find(
      (item) => item.status === 'Up' && item.neighborId,
    )
    if (port) return { device, port }
  }
  throw new Error('The fixture has no linked port')
}

// A port that is down and has neither neighbor nor configured endpoint.
function idlePort() {
  for (const device of devices) {
    const port = portsOf(devices, device).find(
      (item) => item.status === 'Down' && !item.neighborId && !item.endpoint,
    )
    if (port) return { device, port }
  }
  throw new Error('The fixture has no idle port')
}

describe('global search in German', () => {
  it('names the groups, the empty and hint texts, and the site counts', async () => {
    const { host } = await mountSearchClosed({ locale: 'de', fleet: devices })
    expect(host.querySelector('.search-trigger')?.textContent).toContain(
      'Suchen',
    )

    await openSearch(host)
    expect(textOf('.search-empty')).toEqual([
      'Name, IP-Adresse, MAC-Adresse oder Port eingeben.',
    ])
    expect(
      document.body
        .querySelector('[role="combobox"]')
        ?.getAttribute('placeholder'),
    ).toBe('Mandanten, Standorte, Geräte, Clients, Schnittstellen suchen…')
    const hints = document.body.querySelector('.search-hints')?.textContent
    for (const word of ['navigieren', 'öffnen', 'nebeneinander', 'in das Dock'])
      expect(hints).toContain(word)

    await typeQuery('zzzz')
    expect(textOf('.search-empty')).toEqual(['Keine Treffer für „zzzz“.'])

    await typeQuery('aurora')
    expect(headings()).toContain('Mandanten')
    expect(details()).toContain('2 Standorte')

    await typeQuery('meridian')
    expect(details()).toContain('1 Standort')

    await typeQuery('berlin')
    expect(headings()).toEqual(['Standorte', 'Geräte', 'Schnittstellen'])
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('translates a stored recent entry when the locale switches', async () => {
    const { device, port } = linkedPort()
    const neighbor = devices.find((item) => item.id === port.neighborId)
    rememberRecent({ kind: 'tenant', id: 'aurora' })
    rememberRecent({ kind: 'interface', id: device.id, port: port.name })
    const { host, setLocale } = await mountSearchClosed({
      locale: 'en',
      fleet: devices,
    })

    await openSearch(host)
    expect(recentHeading()).toContain('Recent')
    expect(details()).toEqual([`Up · to ${neighbor?.name}`, '2 sites'])

    await setLocale('de')
    expect(recentHeading()).toContain('Zuletzt')
    expect(details()).toEqual([
      `Verbunden · zu ${neighbor?.name}`,
      '2 Standorte',
    ])

    await typeQuery('berlin')
    expect(headings()).toEqual(['Standorte', 'Geräte', 'Schnittstellen'])
    await setLocale('en')
    expect(headings()).toEqual(['Sites', 'Devices', 'Interfaces'])
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('drops a recent entry whose object no longer exists', async () => {
    rememberRecent({
      kind: 'interface',
      id: 'missing-device',
      port: 'ge-0/0/1',
    })
    rememberRecent({ kind: 'tenant', id: 'removed-tenant' })
    const { host } = await mountSearchClosed({ locale: 'de', fleet: devices })

    await openSearch(host)
    expect(details()).toEqual([])
    expect(textOf('.search-empty')).toEqual([
      'Name, IP-Adresse, MAC-Adresse oder Port eingeben.',
    ])
  })

  it('marks docked device page titles and identifier details with translate="no"', async () => {
    const dockedDevicePage: SearchPage = {
      id: 'tab:dev-1',
      title: 'berlin-gw-01',
      icon: 'device',
      identifier: true,
      parts: [{ text: 'Im Dock' }, { text: 'Berlin Mitte', identifier: true }],
    }
    const { host } = await mountSearchClosed({
      locale: 'de',
      fleet: devices,
      pages: [...pages, dockedDevicePage],
    })

    await openSearch(host)
    await typeQuery('berlin-gw-01')

    const pageItem = [...document.body.querySelectorAll('.search-result')].find(
      (item) => item.textContent?.includes('Im Dock · Berlin Mitte'),
    )
    expect(pageItem).toBeDefined()
    expect(
      pageItem?.querySelector('strong span')?.getAttribute('translate'),
    ).toBe('no')
    const pageDetailFacts = [
      ...(pageItem?.querySelectorAll('small span[translate="no"]') ?? []),
    ]
    expect(pageDetailFacts.map((s) => s.textContent)).toContain('Berlin Mitte')

    const deviceItem = [
      ...document.body.querySelectorAll('.search-result'),
    ].find(
      (item) =>
        item.textContent?.includes('Gateway') &&
        item.textContent?.includes('berlin-gw-01'),
    )
    expect(deviceItem).toBeDefined()
    const deviceDetailNoTrans = [
      ...(deviceItem?.querySelectorAll('small span[translate="no"]') ?? []),
    ]
    expect(deviceDetailNoTrans.map((s) => s.textContent)).toContain('10.20.0.1')
    expect(deviceDetailNoTrans.map((s) => s.textContent)).toContain(
      'Berlin Mitte',
    )
  })

  it('renders an idle port with no dangling separator', async () => {
    const { device, port } = idlePort()
    rememberRecent({ kind: 'interface', id: device.id, port: port.name })
    const { host } = await mountSearchClosed({
      locale: 'de',
      fleet: devices,
    })

    await openSearch(host)
    const result = [...document.body.querySelectorAll('.search-result')].find(
      (item) => item.textContent?.includes(port.name),
    )
    expect(result).toBeDefined()
    const detail = result?.querySelector('small')?.textContent?.trim()
    expect(detail).toBe('Getrennt')
    expect(detail).not.toContain('·')
  })

  it('filters pages on words in parts', async () => {
    const bespokePage: SearchPage = {
      id: 'bespoke',
      title: 'Bespoke Title',
      icon: 'dashboard',
      parts: [{ text: 'Im Dock' }, { text: 'Alle' }],
    }
    const { host } = await mountSearchClosed({
      locale: 'de',
      fleet: devices,
      pages: [...pages, bespokePage],
    })

    await openSearch(host)
    await typeQuery('im dock · alle')
    expect(
      [...document.body.querySelectorAll('.search-result strong')].map((el) =>
        el.textContent?.trim(),
      ),
    ).toContain('Bespoke Title')
  })

  it('marks device name in docked pair title with translate="no" and leaves page label unmarked', async () => {
    const pairPage: SearchPage = {
      id: 'pair:1',
      title: 'berlin-gw-01 + Devices',
      icon: 'split',
      pair: {
        first: { label: 'berlin-gw-01', name: true },
        second: { label: 'Devices', name: false },
      },
      parts: [{ text: 'Docked' }],
    }
    const { host } = await mountSearchClosed({
      locale: 'de',
      fleet: devices,
      pages: [...pages, pairPage],
    })

    await openSearch(host)
    await typeQuery('berlin-gw-01')

    const pairItem = [...document.body.querySelectorAll('.search-result')].find(
      (item) => item.textContent?.includes('berlin-gw-01 + Devices'),
    )
    expect(pairItem).toBeDefined()
    const titleElement = pairItem?.querySelector('strong')
    expect(titleElement?.getAttribute('translate')).toBeNull()

    const markedNames = [
      ...(titleElement?.querySelectorAll('span[translate="no"]') ?? []),
    ].map((s) => s.textContent)
    expect(markedNames).toEqual(['berlin-gw-01'])

    const unmarkedSpans = [
      ...(titleElement?.querySelectorAll('span:not([translate])') ?? []),
    ].map((s) => s.textContent)
    expect(unmarkedSpans).toContain('Devices')
  })

  it('satisfies the unmarkedIdentifiers property across search results mounted through FleetView', async () => {
    const offline = devices.find((device) => device.health === 'Offline')
    if (!offline) throw new Error('The fixture has no offline device')
    const sampleClient = clientsOf(devices)[0]
    if (!sampleClient) throw new Error('The fixture has no sample client')
    const { port: upPort } = linkedPort()
    const { port: downPort } = idlePort()

    localStorage.setItem(
      DOCK_KEY,
      JSON.stringify([
        { id: 'tab:page', location: { path: '/devices', query: {} } },
        {
          id: 'tab:pair',
          location: { path: `/devices/${offline.id}`, query: {} },
          beside: { path: '/clients', query: {} },
        },
      ]),
    )

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
    const i18n = createWebI18n('de')
    const app = createApp({
      render() {
        return h(UiAppRoot, {}, () => h(FleetView))
      },
    })
    await router.push('/devices')
    app.use(i18n)
    app.use(router)
    app.directive('ai-target', createAiTargetDirective(registry))
    app.provide(aiRegistryKey, registry)
    await router.isReady()
    app.mount(host)
    dispose = () => app.unmount()
    await settle()

    const trigger = host.querySelector<HTMLButtonElement>('.search-trigger')
    if (!trigger) throw new Error('Missing search trigger button')
    trigger.click()
    await settle()

    const identifiers = fixtureIdentifiers()

    const probes = [
      { query: offline.name, group: 'Geräte' },
      { query: sampleClient.hostname, group: 'Clients' },
      { query: upPort.name, group: 'Schnittstellen' },
      { query: downPort.name, group: 'Schnittstellen' },
      { query: 'aurora', group: 'Mandanten' },
      { query: 'berlin', group: 'Standorte' },
    ]

    for (const { query, group } of probes) {
      await typeQuery(query)
      const groupEl = [
        ...document.body.querySelectorAll<HTMLElement>('[role="group"]'),
      ].find(
        (el) =>
          el
            .querySelector('[id^="reka-combobox-group-label"]')
            ?.textContent?.trim() === group,
      )
      if (!groupEl) {
        throw new Error(`Group "${group}" not found for query "${query}"`)
      }
      const result = groupEl.querySelector('.search-result')
      if (!result) {
        throw new Error(
          `No .search-result in group "${group}" for query "${query}"`,
        )
      }
      expect(unmarkedIdentifiers(document.body, identifiers)).toEqual([])
    }

    await typeQuery(offline.name)
    const pairResult = [
      ...document.body.querySelectorAll('.search-result'),
    ].find((item) => item.textContent?.includes('Paar im Dock'))
    expect(pairResult).toBeDefined()
    const clientsSpan = [
      ...(pairResult?.querySelectorAll('strong span') ?? []),
    ].find((s) => s.textContent?.trim() === 'Clients')
    expect(clientsSpan).toBeDefined()
    expect(clientsSpan?.getAttribute('translate')).toBeNull()
    expect(unmarkedIdentifiers(document.body, identifiers)).toEqual([])

    await typeQuery('geräte')
    const pageTab = [...document.body.querySelectorAll('.search-result')].find(
      (item) => item.textContent?.includes('Alle Standorte'),
    )
    expect(pageTab).toBeDefined()
    expect(
      pageTab?.querySelector('strong span')?.getAttribute('translate'),
    ).toBeNull()
    const allSitesDetail = [
      ...(pageTab?.querySelectorAll('small span') ?? []),
    ].find((s) => s.textContent?.includes('Alle Standorte'))
    expect(allSitesDetail).toBeDefined()
    expect(allSitesDetail?.getAttribute('translate')).toBeNull()
    expect(allSitesDetail?.closest('[translate]')).toBeNull()
  }, 15000)

  it('renders no dangling separator for a client row with an empty field', async () => {
    vi.spyOn(clientsModule, 'clientsOf').mockReturnValue([
      {
        id: 'incomplete-client',
        hostname: 'thinkpad-incomplete',
        address: '',
        mac: '00:11:22:33:44:55',
        deviceId: devices[0].id,
        band: '5 GHz',
        signal: -65,
        throughput: 300,
      },
    ])
    const { host } = await mountSearchClosed({
      fleet: devices,
    })
    const trigger = host.querySelector<HTMLButtonElement>('.search-trigger')
    if (!trigger) throw new Error('Missing search trigger button')
    trigger.click()
    await settle()

    const input =
      document.body.querySelector<HTMLInputElement>('[role="combobox"]')
    if (!input) throw new Error('Global search input did not open.')

    input.value = 'thinkpad-incomplete'
    input.dispatchEvent(new InputEvent('input', { bubbles: true }))
    await settle()

    const result = document.body.querySelector('.search-result')
    if (!result)
      throw new Error('No .search-result found for thinkpad-incomplete')

    const small = result.querySelector('small')
    if (!small) throw new Error('Missing small element in search result')
    expect(small.textContent?.trim()).toBe('')
    expect(result.textContent).not.toContain('·')
  })
})
