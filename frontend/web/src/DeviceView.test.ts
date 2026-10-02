// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { computed, createApp, h, nextTick, ref } from 'vue'
import DeviceView from './DeviceView.vue'
import { pageContext, pageFor } from './navigation/page'
import { workspaceContext } from './navigation/workspace'
import { devices, moveDevice, sites, tenants } from './domain/fleet'
import type { Device } from './domain/fleet'
import { createAiRegistry, createAiTargetDirective } from './ai'
import type { AiRegistry } from './ai'
import { aiRegistryKey } from './ui/ai/context'
import { createWebI18n } from './i18n'

let dispose = () => {}
let registry: AiRegistry

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
})

async function mountDeviceView(device: Device, fleet: Device[]) {
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
    message: ref(''),
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
  app.use(createWebI18n())
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
