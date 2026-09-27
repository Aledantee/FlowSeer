// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'

let dispose = () => {}
// happy-dom has no Web Animations API, so the view runs as it does for a
// user who asked for reduced motion.
beforeEach(() =>
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('reduce'),
    media: query,
    addEventListener() {},
    removeEventListener() {},
  })),
)
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})
async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/:view(dashboard|devices|sites|topology|components)',
        component: FleetView,
      },
    ],
  })
  await router.push(path)
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ template: '<RouterView />' })
  app.use(router)
  app.mount(host)
  dispose = () => app.unmount()
  await router.isReady()
  await nextTick()
  return { host, router }
}

describe('fleet view', () => {
  it('reports an unknown site as an error instead of a healthy empty scope', async () => {
    const { host } = await mountAt('/dashboard?site=nowhere')
    expect(host.textContent).toContain('Scope not found')
    expect(host.textContent).not.toContain(
      'Every device in this scope is healthy',
    )
    expect(host.querySelector('.metrics')).toBeNull()
  })
  it('shows no traffic reading for an offline device', async () => {
    const { host } = await mountAt('/devices?search=cologne-ap-02')
    const cell = host.querySelector('tbody .traffic')
    expect(cell?.textContent).toContain('—')
    expect(cell?.textContent).not.toContain('Mbps')
  })
  it('clears search and status but keeps the tenant and site', async () => {
    const { host, router } = await mountAt(
      '/devices?tenant=aurora-de&site=berlin&search=nothing-matches',
    )
    const clear = [...host.querySelectorAll('button')].find(
      (item) => item.textContent?.trim() === 'Clear search and status',
    )
    clear?.click()
    await new Promise((resolve) => setTimeout(resolve))
    expect(router.currentRoute.value.query).toEqual({
      tenant: 'aurora-de',
      site: 'berlin',
    })
  })
  it('lists devices that need attention first', async () => {
    const { host } = await mountAt('/devices')
    const names = [...host.querySelectorAll('tbody tr strong')].map(
      (item) => item.textContent,
    )
    expect(names.slice(0, 2)).toEqual(['cologne-ap-02', 'hamburg-ap-01'])
  })
  it('opens the dashboard on attention, with the totals in the heading line', async () => {
    const { host } = await mountAt('/dashboard')
    expect(host.querySelector('.metrics')).toBeNull()
    expect(host.querySelector('.page-heading p')?.textContent).toContain(
      '16 devices',
    )
    expect(host.querySelector('.dashboard h2')?.textContent).toBe(
      'Needs attention',
    )
  })
  it('fills in the tenant of a selected site so the scope never reads all tenants', async () => {
    const { host, router } = await mountAt('/dashboard?site=berlin')
    await new Promise((resolve) => setTimeout(resolve))
    expect(router.currentRoute.value.query.tenant).toBe('aurora-de')
    expect(host.querySelector('.page-heading p')?.textContent).toContain(
      'Aurora Germany',
    )
    expect(host.querySelector('.nav-count')?.textContent?.trim()).toBe('4')
  })
  it('shows how long ago each device last answered and sorts the stalest first', async () => {
    const { host } = await mountAt('/devices')
    const header = [...host.querySelectorAll('th button')].find((item) =>
      item.textContent?.includes('Last answered'),
    ) as HTMLButtonElement | undefined
    header?.click()
    await nextTick()
    const first = host.querySelector('tbody tr')
    expect(first?.querySelector('strong')?.textContent).toBe('cologne-ap-02')
    expect(first?.querySelector('.seen')?.textContent?.trim()).toBe(
      '38 min ago',
    )
  })
  it('lets a phone device card announce its health, site, and age', async () => {
    const { host } = await mountAt('/devices?search=cologne-ap-02')
    const card = host.querySelector('.mobile-devices button')
    expect(card?.getAttribute('aria-label')).toBeNull()
    expect(card?.textContent).toContain('Offline')
    expect(card?.textContent).toContain('Cologne Central')
    expect(card?.textContent).toContain('38 min ago')
  })
})
