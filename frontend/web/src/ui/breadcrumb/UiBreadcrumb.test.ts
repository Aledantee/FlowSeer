// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import UiBreadcrumb from './UiBreadcrumb.vue'
import UiBreadcrumbEllipsis from './UiBreadcrumbEllipsis.vue'
import UiBreadcrumbSeparator from './UiBreadcrumbSeparator.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

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

function mountBreadcrumb(
  props: Record<string, unknown> = {},
  localeOrI18n: WebLocale | ReturnType<typeof createWebI18n> = 'en',
) {
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
  if (typeof localeOrI18n === 'string') {
    app.use(createWebI18n(localeOrI18n))
  } else {
    app.use(localeOrI18n)
  }
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

  it('asserts German accessible names through the DOM', () => {
    const host = mountBreadcrumb({ collapsed: true }, 'de')

    const nav = host.querySelector('nav[aria-label="Brotkrümelnavigation"]')
    expect(nav).not.toBeNull()

    const ellipsisTrigger = host.querySelector(
      'button[aria-label="Eingeklappte Brotkrümel umschalten"]',
    )
    expect(ellipsisTrigger).not.toBeNull()

    const separators = host.querySelectorAll('[aria-hidden="true"]')
    expect(separators.length).toBeGreaterThan(0)
    expect(separators[0]?.textContent?.trim()).toBe('/')
  })

  it('preserves explicit ariaLabel, toggleLabel, and separator overrides including empty strings', () => {
    const host = mountBreadcrumb(
      {
        collapsed: true,
        ariaLabel: '',
      },
      'de',
    )

    const nav = host.querySelector('nav')
    expect(nav?.getAttribute('aria-label')).toBe('')

    dispose()
    document.body.replaceChildren()

    const customHost = mountBreadcrumb(
      {
        collapsed: true,
        ariaLabel: 'Custom Nav',
      },
      'de',
    )
    const customNav = customHost.querySelector('nav[aria-label="Custom Nav"]')
    expect(customNav).not.toBeNull()

    dispose()
    document.body.replaceChildren()

    const ellipsisHost = document.createElement('div')
    document.body.append(ellipsisHost)
    const ellipsisApp = createApp({
      render() {
        return h(UiBreadcrumbEllipsis, { toggleLabel: 'X' })
      },
    })
    ellipsisApp.use(createWebI18n('de'))
    ellipsisApp.mount(ellipsisHost)
    dispose = () => ellipsisApp.unmount()

    const toggleBtn = ellipsisHost.querySelector('button[aria-label="X"]')
    expect(toggleBtn).not.toBeNull()

    dispose()
    document.body.replaceChildren()

    const emptyEllipsisHost = document.createElement('div')
    document.body.append(emptyEllipsisHost)
    const emptyEllipsisApp = createApp({
      render() {
        return h(UiBreadcrumbEllipsis, { toggleLabel: '' })
      },
    })
    emptyEllipsisApp.use(createWebI18n('de'))
    emptyEllipsisApp.mount(emptyEllipsisHost)
    dispose = () => emptyEllipsisApp.unmount()

    const emptyToggleBtn = emptyEllipsisHost.querySelector('button')
    if (!emptyToggleBtn) {
      throw new Error('Expected ellipsis toggle button')
    }
    expect(emptyToggleBtn.getAttribute('aria-label')).toBe('')

    dispose()
    document.body.replaceChildren()

    const sepHost = document.createElement('div')
    document.body.append(sepHost)
    const sepApp = createApp({
      render() {
        return h(UiBreadcrumbSeparator, { separator: '>' })
      },
    })
    sepApp.use(createWebI18n('de'))
    sepApp.mount(sepHost)
    dispose = () => sepApp.unmount()

    expect(sepHost.textContent?.trim()).toBe('>')

    dispose()
    document.body.replaceChildren()

    const emptySepHost = document.createElement('div')
    document.body.append(emptySepHost)
    const emptySepApp = createApp({
      render() {
        return h(UiBreadcrumbSeparator, { separator: '' })
      },
    })
    emptySepApp.use(createWebI18n('de'))
    emptySepApp.mount(emptySepHost)
    dispose = () => emptySepApp.unmount()

    expect(emptySepHost.textContent).toBe('')
  })

  it('resolves separator default from catalog', () => {
    const i18n = createWebI18n('en')
    i18n.global.mergeLocaleMessage('en', {
      ui: { breadcrumbSeparator: { separator: '•' } },
    })
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render() {
        return h(UiBreadcrumbSeparator)
      },
    })
    app.use(i18n)
    app.mount(host)
    dispose = () => app.unmount()

    const separator = host.querySelector('li')
    if (!separator) {
      throw new Error('Expected separator element')
    }
    try {
      expect(separator.textContent?.trim()).toBe('•')
    } finally {
      i18n.global.mergeLocaleMessage('en', {
        ui: { breadcrumbSeparator: { separator: '/' } },
      })
    }
  })

  it('updates breadcrumb defaults on live locale change and preserves explicit overrides', async () => {
    const i18n = createWebI18n('en')
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render() {
        return h('div', [
          h('div', { class: 'default-container' }, [
            h(UiBreadcrumb, { items: sampleItems, collapsed: false }),
            h(UiBreadcrumbEllipsis, { items: [{ label: 'Fleet' }] }),
            h(UiBreadcrumbSeparator),
          ]),
          h('div', { class: 'overridden-container' }, [
            h(UiBreadcrumb, {
              items: sampleItems,
              collapsed: false,
              ariaLabel: 'Custom Nav',
            }),
            h(UiBreadcrumbEllipsis, {
              items: [{ label: 'Fleet' }],
              toggleLabel: 'Custom Ellipsis',
            }),
            h(UiBreadcrumbSeparator, { separator: '>' }),
          ]),
        ])
      },
    })
    app.use(i18n)
    app.mount(host)
    dispose = () => app.unmount()
    await nextTick()

    const defaultNav = host.querySelector('.default-container nav')
    const defaultTrigger = host.querySelector('.default-container button')
    const defaultSeparator = host.querySelector('.default-container > li')
    const customNav = host.querySelector('.overridden-container nav')
    const customTrigger = host.querySelector('.overridden-container button')
    const customSeparator = host.querySelector('.overridden-container > li')

    if (
      !defaultNav ||
      !defaultTrigger ||
      !defaultSeparator ||
      !customNav ||
      !customTrigger ||
      !customSeparator
    ) {
      throw new Error('Expected default and overridden breadcrumb elements')
    }

    expect(defaultNav.getAttribute('aria-label')).toBe('Breadcrumb')
    expect(defaultTrigger.getAttribute('aria-label')).toBe(
      'Toggle collapsed breadcrumbs',
    )
    expect(defaultSeparator.textContent?.trim()).toBe('/')
    expect(customNav.getAttribute('aria-label')).toBe('Custom Nav')
    expect(customTrigger.getAttribute('aria-label')).toBe('Custom Ellipsis')
    expect(customSeparator.textContent?.trim()).toBe('>')

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(defaultNav.getAttribute('aria-label')).toBe('Brotkrümelnavigation')
    expect(defaultTrigger.getAttribute('aria-label')).toBe(
      'Eingeklappte Brotkrümel umschalten',
    )
    expect(defaultSeparator.textContent?.trim()).toBe('/')
    expect(customNav.getAttribute('aria-label')).toBe('Custom Nav')
    expect(customTrigger.getAttribute('aria-label')).toBe('Custom Ellipsis')
    expect(customSeparator.textContent?.trim()).toBe('>')
  })
})
