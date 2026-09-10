import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import TenantSwitcher from './TenantSwitcher.vue'

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
    expect(html).toContain('aria-label="Tenant scope"')
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
})
