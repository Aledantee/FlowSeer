// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiBreadcrumb from './UiBreadcrumb.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

const sampleItems = [
  { label: 'Fleet', href: '#fleet' },
  { label: 'Europe West', href: '#europe' },
  { label: 'Berlin Core', href: '#berlin' },
  { label: 'Gateway 01', current: true },
]

function mountBreadcrumb(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiBreadcrumb, {
        items: sampleItems,
        ...props,
      })
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiBreadcrumb', () => {
  it('renders semantic <nav aria-label="Breadcrumb"> and <ol>', () => {
    const host = mountBreadcrumb({ collapsed: false })

    const nav = host.querySelector('nav[aria-label="Breadcrumb"]')
    expect(nav).not.toBeNull()

    const ol = nav?.querySelector('ol')
    expect(ol).not.toBeNull()
  })

  it('current page element receives aria-current="page"', () => {
    const host = mountBreadcrumb({ collapsed: false })

    const current = host.querySelector('[aria-current="page"]')
    expect(current).not.toBeNull()
    expect(current?.textContent).toContain('Gateway 01')
  })

  it('separators receive aria-hidden="true"', () => {
    const host = mountBreadcrumb({ collapsed: false })

    const separators = host.querySelectorAll('[aria-hidden="true"]')
    expect(separators.length).toBeGreaterThan(0)
    expect(separators[0]?.textContent).toContain('/')
  })

  it('on narrow viewports where breadcrumb exceeds container width, middle items collapse into UiBreadcrumbEllipsis', () => {
    const host = mountBreadcrumb({ collapsed: true })

    const ellipsisTrigger = host.querySelector(
      'button[aria-label="Toggle collapsed breadcrumbs"]',
    )
    expect(ellipsisTrigger).not.toBeNull()

    const list = host.querySelector('ol')
    expect(list?.textContent).toContain('Fleet')
    expect(list?.textContent).toContain('Gateway 01')
    expect(list?.textContent).not.toContain('Europe West')
    expect(list?.textContent).not.toContain('Berlin Core')
  })

  it('clicking ellipsis dropdown opens menu containing links to collapsed items', async () => {
    const host = mountBreadcrumb({ collapsed: true })

    const ellipsisTrigger = host.querySelector<HTMLButtonElement>(
      'button[aria-label="Toggle collapsed breadcrumbs"]',
    )
    expect(ellipsisTrigger).not.toBeNull()

    ellipsisTrigger?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const menu = document.body.querySelector('[role="menu"]')
    expect(menu).not.toBeNull()
    expect(menu?.textContent).toContain('Europe West')
    expect(menu?.textContent).toContain('Berlin Core')

    const links = menu?.querySelectorAll('a')
    expect(links?.length).toBe(2)
    expect(links?.[0]?.getAttribute('href')).toBe('#europe')
    expect(links?.[1]?.getAttribute('href')).toBe('#berlin')
  })
})
