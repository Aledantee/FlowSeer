// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { computed, createApp, h, nextTick, ref } from 'vue'
import DeviceView from './DeviceView.vue'
import { pageContext, pageFor } from './navigation/page'
import { workspaceContext } from './navigation/workspace'
import { devices, moveDevice, sites, tenants } from './domain/fleet'
import type { Device } from './domain/fleet'

let dispose = () => {}

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
  app.provide(pageContext, page)
  app.provide(workspaceContext, workspace)
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
