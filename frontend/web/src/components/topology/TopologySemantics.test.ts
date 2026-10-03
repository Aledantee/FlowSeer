// @vitest-environment happy-dom
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  computed,
  createApp,
  defineComponent,
  h,
  nextTick,
  provide,
  ref,
} from 'vue'
import type { Component } from 'vue'
import { Position } from '@vue-flow/core'
import { devices, linksOf, sites, tenants } from '../../domain/fleet'
import type { Device, Link } from '../../domain/fleet'
import {
  linkDetailsOf,
  portDetailsOf,
  telemetryOf,
} from '../../domain/telemetry'
import { pageContext, pageFor } from '../../navigation/page'
import { workspaceContext } from '../../navigation/workspace'
import { topologyLive } from './live'
import type { Selection } from './live'
import { createAiRegistry, createAiTargetDirective } from '../../ai'
import type { AiRegistry } from '../../ai'
import TopologyInspector from './TopologyInspector.vue'
import TopologyLink from './TopologyLink.vue'
import TopologyNode from './TopologyNode.vue'
import en from '../../i18n/locales/en.json'
import { createWebI18n } from '../../i18n'
import type { WebLocale } from '../../i18n'
import { i18nWarnings } from '../../i18n/testing'

vi.mock('@vue-flow/core', async () => {
  const vue = await import('vue')
  return {
    BaseEdge: vue.defineComponent({
      inheritAttrs: false,
      setup(_, { attrs }) {
        return () => vue.h('path', { ...attrs, class: 'vue-flow__edge-path' })
      },
    }),
    EdgeLabelRenderer: vue.defineComponent({
      setup(_, { slots }) {
        return () => slots.default?.()
      },
    }),
    Handle: vue.defineComponent({
      setup(_, { attrs }) {
        return () => vue.h('span', attrs)
      },
    }),
    Position: { Top: 'top', Right: 'right', Bottom: 'bottom', Left: 'left' },
    getSmoothStepPath: () => ['M 0 0 L 10 10'],
  }
})

let dispose = () => {}
let registry: AiRegistry
let i18n: ReturnType<typeof createWebI18n>
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
})

function siteName(id: string): string {
  return sites.find((site) => site.id === id)?.name ?? ''
}

interface MountOptions {
  locale?: WebLocale
  selection?: Selection
  fleet?: Device[]
}

function mount(
  component: Component,
  props: Record<string, unknown>,
  options: MountOptions = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const currentDevices = options.fleet ?? devices
  const fleet = computed(() => currentDevices)
  const links = linksOf(currentDevices)
  const selection = ref<Selection | undefined>(options.selection)
  const hovered = ref<string>()
  const location = computed(() => ({ path: '/topology', query: {} }))
  const page = pageFor(
    location,
    () => true,
    async () => {},
  )
  const workspace = {
    fleet: ref(currentDevices),
    message: ref(''),
    reassign: () => {},
    move: ref(undefined),
    undoMove: () => {},
    dismissNotice: () => {},
    siteName,
    tenantName: (siteId: string) =>
      tenants.find((t) => t.id === sites.find((s) => s.id === siteId)?.tenantId)
        ?.name ?? '',
    follow: async () => {},
    activePane: ref('main' as const),
    peek: ref([]),
    sideDeviceId: computed(() => undefined),
  }
  const select = vi.fn((next: Selection | undefined) => {
    selection.value = next
  })
  const app = createApp(
    defineComponent({
      setup() {
        provide(topologyLive, {
          fleet,
          highlighted: computed(() => undefined),
          device: (id: string): Device | undefined =>
            fleet.value.find((device) => device.id === id),
          link: (id: string): Link | undefined =>
            links.find((link) => link.id === id),
          selection,
          hovered,
          select,
        })
        return () => h(component, props)
      },
    }),
  )
  registry = createAiRegistry()
  i18n = createWebI18n(options.locale)
  app.use(i18n)
  app.provide(pageContext, page)
  app.provide(workspaceContext, workspace)
  app.directive('ai-target', createAiTargetDirective(registry))
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('topology assumption semantics', () => {
  it('renders status badges only for failing nodes', () => {
    const healthy = devices.find((device) => device.health === 'Healthy')
    const degraded = devices.find((device) => device.health === 'Degraded')
    if (!healthy || !degraded) throw new Error('Missing health fixtures')

    let host = mount(TopologyNode, { data: { deviceId: healthy.id } })
    expect(host.querySelector('.topology-status')).toBeNull()
    expect(host.textContent).not.toContain('Healthy')
    dispose()

    host = mount(TopologyNode, { data: { deviceId: degraded.id } })
    expect(host.textContent).toContain('Degraded')
    expect(host.querySelector('.bg-warning-surface')).not.toBeNull()
  })

  it('states that rendered links are assumed', () => {
    const link = linksOf(devices)[0]
    if (!link) throw new Error('Missing link fixture')

    const host = mount(TopologyLink, {
      id: link.id,
      sourceX: 0,
      sourceY: 0,
      targetX: 10,
      targetY: 10,
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
      data: { linkId: link.id },
    })

    expect(
      host.querySelector('.topology-link-label')?.getAttribute('aria-label'),
    ).toContain('Assumed link, not yet discovered')
  })

  it('keeps the visible legend and base-link dash in component source', () => {
    const graph = readFileSync(
      path.resolve(__dirname, 'TopologyGraph.vue'),
      'utf8',
    )
    const link = readFileSync(
      path.resolve(__dirname, 'TopologyLink.vue'),
      'utf8',
    )
    const baseRule = link.match(
      /\.topology-link :deep\(\.vue-flow__edge-path\) \{([^}]*)\}/,
    )?.[1]

    expect(graph).toMatch(
      /class="topology-assumption"[^>]*>[\s\S]*t\('view\.topology\.assumption'\)/,
    )
    expect(en.view.topology.assumption).toBe('Assumed link, not yet discovered')
    expect(baseRule).toContain('stroke-dasharray: 4 5')
  })
})

const RELATIVE = { numeric: 'auto', style: 'short' } as const

function linkProps(link: Link) {
  return {
    id: link.id,
    sourceX: 0,
    sourceY: 0,
    targetX: 10,
    targetY: 10,
    sourcePosition: Position.Bottom,
    targetPosition: Position.Top,
    data: { linkId: link.id },
  }
}

// Locates the fixtures each case selects.
function fixtures() {
  const ap = devices.find(
    (device) => device.role === 'access-point' && device.health !== 'Offline',
  )
  const core = linksOf(devices).find(
    (link) => link.capacity === 10_000 && link.health === 'Healthy',
  )
  const degraded = devices.find((device) => device.health === 'Degraded')
  const owner = devices.find((device) => device.id === core?.sourceId)
  const portName = core
    ? linkDetailsOf(devices, core).sourcePort?.name
    : undefined
  if (!ap || !core || !degraded || !owner || !portName) {
    throw new Error('Missing topology fixtures')
  }
  return { ap, core, degraded, owner, portName }
}

describe('topology in German', () => {
  it('labels a node in the active locale', () => {
    const { degraded } = fixtures()

    const host = mount(
      TopologyNode,
      { data: { deviceId: degraded.id } },
      { locale: 'de' },
    )

    expect(
      host.querySelector('.topology-node')?.getAttribute('aria-label'),
    ).toBe(`${degraded.name}, ${degraded.kind}, Beeinträchtigt`)
    expect(
      host.querySelector('strong')?.closest('[translate="no"]'),
    ).not.toBeNull()
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('states the assumption in German on a link label', () => {
    const { core } = fixtures()

    const host = mount(TopologyLink, linkProps(core), { locale: 'de' })

    const label = host
      .querySelector('.topology-link-label')
      ?.getAttribute('aria-label')
    expect(label).toMatch(
      /^Angenommene Verbindung, noch nicht ermittelt\. .+ zu .+, 10G, .+Mbit\/s, Gesund$/,
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('localizes the inspector for a device selection', () => {
    const { ap } = fixtures()
    const booted = telemetryOf(ap).bootedAt ?? 0
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(booted + (3 * 1440 + 4 * 60 + 5) * 60_000)

    const host = mount(
      TopologyInspector,
      { history: { [ap.id]: [1, 2, 3, 4, 5] }, siteName },
      { locale: 'de', selection: { kind: 'device', id: ap.id } },
    )

    const text = host.textContent ?? ''
    for (const field of [
      'Betriebszeit',
      'IP-Adresse',
      'Datenverkehr',
      'Modell',
      'Ressourcen',
      'Arbeitsspeicher',
      'Funkmodule',
      'Kanal',
      'Gerät öffnen',
    ]) {
      expect(text).toContain(field)
    }
    expect(text).toContain('3 T. 4 Std.')
    expect(text).toMatch(/\d\s+Mbit\/s/)
    expect(host.querySelector('aside')?.getAttribute('aria-label')).toBe(
      `Details zu ${ap.name}`,
    )
    expect(host.innerHTML).toContain('Datenverkehr der letzten 5 Messungen')
    expect(
      host.querySelector('header strong')?.closest('[translate="no"]'),
    ).not.toBeNull()
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('localizes the inspector for a link selection', () => {
    const { core } = fixtures()

    const host = mount(
      TopologyInspector,
      { history: {}, siteName },
      { locale: 'de', selection: { kind: 'link', id: core.id } },
    )

    const text = host.textContent ?? ''
    for (const field of [
      'Geschwindigkeit',
      'Downstream',
      'Upstream',
      'Latenz',
      'CRC-Fehler',
      'Glasfaser',
      'Auslastung',
    ]) {
      expect(text).toContain(field)
    }
    expect(text).toMatch(/10\s+Gbit\/s\s+·\s+Vollduplex/)
    expect(text).toMatch(/von\s+10\.000\s+Mbit\/s/)
    expect(host.querySelector('aside')?.getAttribute('aria-label')).toBe(
      'Verbindungsdetails',
    )
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('localizes the inspector for a port selection', () => {
    const { owner, portName } = fixtures()
    const changed = portDetailsOf(devices, owner, portName)?.lastChange ?? 0
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(changed + 38.5 * 60_000)

    const host = mount(
      TopologyInspector,
      { history: {}, siteName },
      {
        locale: 'de',
        selection: { kind: 'port', id: owner.id, port: portName },
      },
    )

    const text = host.textContent ?? ''
    expect(host.querySelector('dl .port-state')?.textContent).toBe('Verbunden')
    for (const field of [
      'Port-Modus Trunk',
      'Empfangen',
      'Gesendet',
      'Letzte Änderung',
      'Verbunden mit',
    ]) {
      expect(text).toContain(field)
    }
    expect(text).toMatch(/10\s+Gbit\/s\s+·\s+Vollduplex/)
    expect(text).toContain(
      new Intl.RelativeTimeFormat('de', RELATIVE).format(-38, 'minute'),
    )
    expect(
      [...host.querySelectorAll('dd.mono')].every(
        (cell) => cell.closest('[translate="no"]') !== null,
      ),
    ).toBe(true)
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })

  it('follows a locale switch on the inspector', async () => {
    const { ap } = fixtures()

    const host = mount(
      TopologyInspector,
      { history: {}, siteName },
      { selection: { kind: 'device', id: ap.id } },
    )
    expect(host.textContent).toContain('Uptime')
    expect(host.querySelector('aside')?.getAttribute('aria-label')).toBe(
      `${ap.name} details`,
    )

    i18n.global.locale.value = 'de'
    await nextTick()
    expect(host.textContent).toContain('Betriebszeit')
    expect(host.textContent).not.toContain('Uptime')
    expect(host.querySelector('aside')?.getAttribute('aria-label')).toBe(
      `Details zu ${ap.name}`,
    )

    i18n.global.locale.value = 'en'
    await nextTick()
    expect(host.textContent).toContain('Uptime')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})

describe('topology AI targets', () => {
  it('registers a node with its device context', () => {
    const device = devices.find((item) => item.role === 'access-point')
    if (!device) throw new Error('Missing device fixture')

    const customDevice: Device = {
      ...device,
      id: 'dev-custom',
      name: 'custom-dev',
      clients: 1250,
    }

    mount(
      TopologyNode,
      { data: { deviceId: customDevice.id } },
      { locale: 'de', fleet: [customDevice] },
    )

    const node = registry.view(`standalone:topology:device:${customDevice.id}`)
    expect(node?.target.kind).toBe('device')
    expect(node?.target.label).toBe(customDevice.name)
    expect(node?.target.context).toMatchObject({
      name: customDevice.name,
      role: customDevice.role,
      health: customDevice.health,
      address: customDevice.address,
      clients: '1.250',
    })
  })

  it('registers a link label with both endpoint names', () => {
    const link = linksOf(devices)[0]
    if (!link) throw new Error('Missing link fixture')
    const byId = new Map(devices.map((device) => [device.id, device]))

    mount(TopologyLink, {
      id: link.id,
      sourceX: 0,
      sourceY: 0,
      targetX: 10,
      targetY: 10,
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
      data: { linkId: link.id },
    })

    const label = registry.view(`standalone:topology:link:${link.id}`)
    expect(label?.target.kind).toBe('link')
    expect(label?.target.context).toMatchObject({
      health: link.health,
      source: byId.get(link.sourceId)?.name,
      target: byId.get(link.targetId)?.name,
    })
  })
})
