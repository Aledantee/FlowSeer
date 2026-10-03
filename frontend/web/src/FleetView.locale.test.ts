// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { UiAppRoot } from './ui'
import { isMac } from './navigation/shortcuts'
import { DOCK_KEY } from './navigation/dock'
import { devices, filterDevices } from './domain/fleet'
import { createAiRegistry, createAiTargetDirective } from './ai'
import { aiRegistryKey } from './ui/ai/context'
import { createWebI18n } from './i18n'
import type { WebLocale } from './i18n'
import { i18nWarnings } from './i18n/testing'

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
    expect(count).toBeTruthy()
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
