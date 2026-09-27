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
})
