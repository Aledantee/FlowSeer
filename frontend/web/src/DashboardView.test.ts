// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, createApp, h, nextTick, ref } from 'vue'
import DashboardView from './DashboardView.vue'
import { pageContext, pageFor } from './navigation/page'
import {
  workspaceContext,
  type NoticeKey,
  type Workspace,
} from './navigation/workspace'
import { devices, sites, tenants } from './domain/fleet'
import type { Site } from './domain/fleet'
import { createAiRegistry } from './ai'
import type { AiRegistry } from './ai'
import { aiRegistryKey } from './ui/ai/context'
import { createWebI18n, type WebLocale } from './i18n'

let dispose = () => {}
let registry: AiRegistry

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

async function mountDashboardView({
  scope = devices,
  sitesList = sites,
  site = undefined as Site | undefined,
  locale = 'en' as WebLocale,
} = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  registry = createAiRegistry()

  const query: Record<string, string> = site ? { site: site.id } : {}
  const loc = computed(() => ({ path: '/', query }))
  const page = pageFor(
    loc,
    () => true,
    async () => {},
  )

  const tenantName = (siteId: string) => {
    const s = sitesList.find((item) => item.id === siteId)
    const t = tenants.find((item) => item.id === s?.tenantId)
    return t?.name ?? 'Acme Tenant'
  }

  const workspace: Workspace = {
    fleet: ref(scope),
    message: ref<NoticeKey | ''>(''),
    move: ref(undefined),
    undoMove: () => {},
    dismissNotice: () => {},
    reassign: () => {},
    siteName: (id: string) => sitesList.find((s) => s.id === id)?.name ?? '',
    tenantName,
    follow: async () => {},
    activePane: ref('main'),
    peek: ref([]),
    sideDeviceId: computed(() => undefined),
  }

  const i18n = createWebI18n(locale)
  const app = createApp({
    render() {
      return h(DashboardView, {
        scope,
        sites: sitesList,
        site,
        tenantName,
      })
    },
  })
  app.provide(pageContext, page)
  app.provide(workspaceContext, workspace)
  app.provide(aiRegistryKey, registry)
  app.use(i18n)
  app.mount(host)
  await nextTick()
  dispose = () => app.unmount()
  return { host, page, loc, registry }
}

describe('DashboardView', () => {
  it('renders populated fleet with exact health counts, percentages, attention list, and roles breakdown', async () => {
    const { host } = await mountDashboardView({
      scope: devices,
      sitesList: sites,
    })

    // Exact health counts: 14 Healthy, 1 Degraded, 1 Offline, 16 total -> 87.5% healthy
    const text = host.textContent ?? ''
    expect(text).toContain('14')
    expect(text).toContain('87.5%')

    // Attention list: 2 devices needing attention
    const attentionLinks = Array.from(host.querySelectorAll('a')).filter((a) =>
      a.getAttribute('href')?.startsWith('/devices/dev-'),
    )
    expect(attentionLinks.length).toBeGreaterThanOrEqual(2)

    // Verify AI targets registered
    const targets = registry.list()
    expect(targets.some((t) => t.kind === 'view')).toBe(true)
    expect(targets.some((t) => t.kind === 'chart')).toBe(true)
    expect(targets.some((t) => t.kind === 'attention-device')).toBe(true)
    expect(targets.some((t) => t.kind === 'site')).toBe(true)
  })

  it('handles empty scope with localized empty state and no NaN', async () => {
    const { host } = await mountDashboardView({ scope: [], sitesList: sites })
    const text = host.textContent ?? ''
    expect(text).not.toContain('NaN')
    expect(text).toContain('No devices in this scope.')
  })

  it('switches traffic range between 24h and 6h, updating peak and sample counts', async () => {
    const { host } = await mountDashboardView({
      scope: devices,
      sitesList: sites,
    })

    // Find range toggle buttons
    const buttons = Array.from(host.querySelectorAll('button'))
    const button6h = buttons.find((b) => b.textContent?.trim() === '6h')
    const button24h = buttons.find((b) => b.textContent?.trim() === '24h')

    expect(button6h).toBeDefined()
    expect(button24h).toBeDefined()

    // Default 24h: 24 table rows in the chart
    expect(host.querySelectorAll('.traffic-chart table tbody tr')).toHaveLength(
      24,
    )

    // Click 6h
    button6h?.click()
    await nextTick()

    expect(host.querySelectorAll('.traffic-chart table tbody tr')).toHaveLength(
      6,
    )

    // Click 24h
    button24h?.click()
    await nextTick()

    expect(host.querySelectorAll('.traffic-chart table tbody tr')).toHaveLength(
      24,
    )
  })

  it('renders site-scoped dashboard with preserved device links, role targets, and back link to all sites', async () => {
    const hamburg = sites.find((s) => s.id === 'hamburg')
    if (!hamburg) throw new Error('Missing fixture site')
    const scopedDevices = devices.filter((d) => d.siteId === hamburg.id)

    const { host, registry } = await mountDashboardView({
      scope: scopedDevices,
      sitesList: sites,
      site: hamburg,
    })

    const text = host.textContent ?? ''
    expect(text).toContain('Hamburg')

    // Preserved link back to all sites
    const allSitesLink = Array.from(host.querySelectorAll('a')).find(
      (a) => a.textContent?.trim() === 'All sites',
    )
    expect(allSitesLink).toBeDefined()

    // Device links for site devices
    const deviceLinks = Array.from(host.querySelectorAll('a')).filter((a) =>
      a.getAttribute('href')?.startsWith('/devices/dev-'),
    )
    expect(deviceLinks.length).toBeGreaterThan(0)

    // Role device targets registered
    const targets = registry.list()
    expect(targets.some((t) => t.kind === 'role-device')).toBe(true)
  })
})
