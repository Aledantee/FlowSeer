// @vitest-environment happy-dom
import { createApp, createSSRApp, h, nextTick } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, describe, expect, it } from 'vitest'
import TenantSwitcher from './TenantSwitcher.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountApp(renderFn: () => unknown) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: renderFn,
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('tenant switcher', () => {
  it('shows the only available tenant without a dropdown', async () => {
    const html = await renderToString(
      createSSRApp(TenantSwitcher, {
        tenants: [{ id: 'aurora', name: 'Aurora Hospitality' }],
        selected: '',
      }),
    )
    expect(html).toContain('Aurora Hospitality')
    expect(html).not.toContain('<select')
  })
  it('offers switching when multiple tenants are available', async () => {
    const html = await renderToString(
      createSSRApp(TenantSwitcher, {
        tenants: [
          { id: 'aurora', name: 'Aurora Hospitality' },
          { id: 'meridian', name: 'Meridian Workspaces' },
        ],
        selected: 'meridian',
      }),
    )
    expect(html).toContain('aria-haspopup="listbox"')
    expect(html).toContain('Meridian Workspaces')
    expect(html).toContain('aria-label="Tenant scope: Meridian Workspaces"')
  })
  it('labels an unavailable tenant without implying all tenants are selected', async () => {
    const html = await renderToString(
      createSSRApp(TenantSwitcher, {
        tenants: [
          { id: 'aurora', name: 'Aurora Hospitality' },
          { id: 'meridian', name: 'Meridian Workspaces' },
        ],
        selected: 'removed-tenant',
      }),
    )
    expect(html).toContain('aria-label="Tenant scope: Unavailable selection"')
    expect(html).not.toContain('undefined')
  })
  it('mounts in client with trigger button reflecting selected tenant scope', async () => {
    const host = mountApp(() =>
      h(TenantSwitcher, {
        tenants: [
          { id: 'aurora', name: 'Aurora Hospitality' },
          { id: 'meridian', name: 'Meridian Workspaces' },
        ],
        selected: 'aurora',
      }),
    )
    await nextTick()
    const trigger = host.querySelector('button.scope-trigger')
    expect(trigger).not.toBeNull()
    expect(trigger?.getAttribute('aria-label')).toBe('Tenant scope: Aurora Hospitality')
  })
})
