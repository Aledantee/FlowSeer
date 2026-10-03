// @vitest-environment happy-dom
import { createApp, createSSRApp, h, nextTick } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, describe, expect, it } from 'vitest'
import TenantSwitcher from './TenantSwitcher.vue'
import { createWebI18n } from '../i18n'

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
  app.use(createWebI18n())
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('tenant switcher', () => {
  it('shows the only available tenant without a dropdown', async () => {
    const app = createSSRApp(TenantSwitcher, {
      tenants: [{ id: 'aurora', name: 'Aurora Hospitality' }],
      selected: '',
    })
    app.use(createWebI18n())
    const html = await renderToString(app)
    expect(html).toContain('Aurora Hospitality')
    expect(html).not.toContain('<select')
  })
  it('offers switching when multiple tenants are available', async () => {
    const app = createSSRApp(TenantSwitcher, {
      tenants: [
        { id: 'aurora', name: 'Aurora Hospitality' },
        { id: 'meridian', name: 'Meridian Workspaces' },
      ],
      selected: 'meridian',
    })
    app.use(createWebI18n())
    const html = await renderToString(app)
    expect(html).toContain('aria-haspopup="listbox"')
    expect(html).toContain('Meridian Workspaces')
    expect(html).toContain('aria-label="Tenant scope: Meridian Workspaces"')
  })
  it('labels an unavailable tenant without implying all tenants are selected', async () => {
    const app = createSSRApp(TenantSwitcher, {
      tenants: [
        { id: 'aurora', name: 'Aurora Hospitality' },
        { id: 'meridian', name: 'Meridian Workspaces' },
      ],
      selected: 'removed-tenant',
    })
    app.use(createWebI18n())
    const html = await renderToString(app)
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
    expect(trigger?.getAttribute('aria-label')).toBe(
      'Tenant scope: Aurora Hospitality',
    )
  })
  it('mounts in client with trigger button reflecting all tenants scope when selected is empty', async () => {
    const host = mountApp(() =>
      h(TenantSwitcher, {
        tenants: [
          { id: 'aurora', name: 'Aurora Hospitality' },
          { id: 'meridian', name: 'Meridian Workspaces' },
        ],
        selected: '',
      }),
    )
    await nextTick()
    const trigger = host.querySelector('button.scope-trigger')
    expect(trigger).not.toBeNull()
    expect(trigger?.getAttribute('aria-label')).toBe(
      'Tenant scope: All tenants',
    )
    expect(trigger?.getAttribute('tabindex')).toBe('0')
    const ariaControls = trigger?.getAttribute('aria-controls')
    expect(ariaControls).toBeTruthy()
    expect(ariaControls).not.toBe('')
  })
  it('opens dropdown and allows selecting all tenants scope emitting change exactly once', async () => {
    const changes: string[] = []
    const host = mountApp(() =>
      h(TenantSwitcher, {
        tenants: [
          { id: 'aurora', name: 'Aurora Hospitality' },
          { id: 'meridian', name: 'Meridian Workspaces' },
        ],
        selected: 'meridian',
        onChange: (val: string) => {
          changes.push(val)
        },
      }),
    )
    await nextTick()
    const trigger = host.querySelector('button.scope-trigger')
    const ariaControls = trigger?.getAttribute('aria-controls')
    expect(ariaControls).toBeTruthy()
    expect(ariaControls).not.toBe('')

    trigger?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const listbox = document.getElementById(ariaControls!)
    expect(listbox).not.toBeNull()

    const options = document.body.querySelectorAll('[role="option"]')
    expect(options.length).toBe(3)
    expect(options[0]?.textContent).toContain('All tenants')

    options[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(changes).toHaveLength(1)
    expect(changes[0]).toBe('')
  })
})
