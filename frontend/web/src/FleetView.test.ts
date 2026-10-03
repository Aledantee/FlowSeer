// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { UiAppRoot } from './ui'
import { isMac } from './navigation/shortcuts'
import { createAiRegistry, createAiTargetDirective } from './ai'
import type { AiRegistry } from './ai'
import { aiRegistryKey } from './ui/ai/context'
import { createWebI18n } from './i18n'

let dispose = () => {}
let registry: AiRegistry

// Reduced motion lets these cases assert the static end state without
// waiting on animation while keeping desktop media queries matched.
beforeEach(() =>
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('reduce') || query.includes('min-width'),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })),
)

beforeEach(() =>
  vi
    .spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    .mockImplementation(function (this: HTMLElement) {
      const collapsed =
        this instanceof HTMLElement &&
        this.closest('.shell')?.classList.contains('sidebar-collapsed')
      const width = collapsed ? 64 : 204
      return {
        bottom: 64,
        height: 64,
        left: 0,
        right: width,
        top: 0,
        width,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      } as DOMRect
    }),
)

afterEach(() => {
  dispose()
  dispose = () => {}
  localStorage.clear()
  sessionStorage.clear()
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function mountAt(path: string) {
  const host = document.createElement('div')
  document.body.append(host)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/:view(dashboard|devices|clients|sites|topology)',
        component: FleetView,
      },
      { path: '/devices/:deviceId', component: FleetView },
    ],
  })
  registry = createAiRegistry()
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () => h(FleetView))
    },
  })
  await router.push(path)
  app.use(createWebI18n())
  app.use(router)
  app.directive('ai-target', createAiTargetDirective(registry))
  app.provide(aiRegistryKey, registry)
  await router.isReady()
  app.mount(host)
  dispose = () => app.unmount()
  await settle()
  return { host, router }
}

async function mountFleet(path: string) {
  return mountAt(path)
}

function workspaceShortcut(repeat = false) {
  return new KeyboardEvent('keydown', {
    key: '\\',
    code: 'Backslash',
    bubbles: true,
    cancelable: true,
    repeat,
    ...(isMac() ? { metaKey: true } : { ctrlKey: true }),
  })
}

function tableFor(host: HTMLElement, headingId: string) {
  const table = host.querySelector(`#${headingId}`)?.closest('section')
  if (!table) throw new Error(`Missing ${headingId} table`)
  return table
}

function column(table: Element, label: string) {
  const headers = [...table.querySelectorAll('th')]
  const index = headers.findIndex((item) => item.textContent?.trim() === label)
  if (index < 0) throw new Error(`Missing ${label} column`)
  return {
    head: headers[index],
    cells: [...table.querySelectorAll(`tbody tr > :nth-child(${index + 1})`)],
  }
}

describe('FleetView workspace shortcuts', () => {
  it('runs a held workspace command once per key press', async () => {
    await mountFleet('/dashboard')

    const pressed = workspaceShortcut()
    window.dispatchEvent(pressed)
    await settle()

    expect(document.querySelector('.panes.split')).not.toBeNull()
    expect(pressed.defaultPrevented).toBe(true)

    const repeated = workspaceShortcut(true)
    window.dispatchEvent(repeated)
    await settle()

    expect(document.querySelector('.panes.split')).not.toBeNull()
    expect(repeated.defaultPrevented).toBe(true)
  })

  it('keeps repeated unmodified arrow navigation available', async () => {
    await mountFleet('/devices')

    document
      .querySelector<HTMLButtonElement>('[aria-label^="Peek at "]')
      ?.click()
    await settle()
    const firstTitle = document.title

    const repeated = new KeyboardEvent('keydown', {
      key: 'ArrowDown',
      code: 'ArrowDown',
      bubbles: true,
      cancelable: true,
      repeat: true,
    })
    window.dispatchEvent(repeated)
    await settle()

    expect(repeated.defaultPrevented).toBe(true)
    expect(document.title).not.toBe(firstTitle)
  })

  it('does not run a workspace shortcut behind a modal dialog', async () => {
    await mountFleet('/dashboard')

    const reportBug = document.querySelector<HTMLButtonElement>(
      '[aria-label="Report bug"]',
    )
    expect(reportBug).not.toBeNull()
    reportBug?.click()
    await settle()

    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    const blocked = workspaceShortcut()
    window.dispatchEvent(blocked)
    await settle()

    expect(document.querySelector('.panes.split')).toBeNull()
    expect(blocked.defaultPrevented).toBe(false)

    document
      .querySelector<HTMLButtonElement>('[role="dialog"] [aria-label="Close"]')
      ?.click()
    await settle()

    const popper = document.createElement('div')
    popper.dataset.rekaPopperContentWrapper = ''
    const popover = document.createElement('div')
    popover.setAttribute('role', 'dialog')
    popover.dataset.state = 'open'
    popper.append(popover)
    document.body.append(popper)

    const allowed = workspaceShortcut()
    window.dispatchEvent(allowed)
    await settle()

    expect(document.querySelector('.panes.split')).not.toBeNull()
    expect(allowed.defaultPrevented).toBe(true)
  })

  it('leaves a peek open when Escape belongs to an alert dialog', async () => {
    await mountFleet('/devices')

    document
      .querySelector<HTMLButtonElement>('[aria-label^="Peek at "]')
      ?.click()
    await settle()
    expect(document.querySelector('.panes.split')).not.toBeNull()

    const alertDialog = document.createElement('div')
    alertDialog.setAttribute('role', 'alertdialog')
    alertDialog.dataset.state = 'open'
    document.body.append(alertDialog)

    const blocked = new KeyboardEvent('keydown', {
      key: 'Escape',
      code: 'Escape',
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(blocked)
    await settle()

    expect(document.querySelector('.panes.split')).not.toBeNull()
    expect(blocked.defaultPrevented).toBe(false)

    alertDialog.remove()
    const allowed = new KeyboardEvent('keydown', {
      key: 'Escape',
      code: 'Escape',
      bubbles: true,
      cancelable: true,
    })
    window.dispatchEvent(allowed)
    await settle()

    expect(document.querySelector('.panes.split')).toBeNull()
    expect(allowed.defaultPrevented).toBe(true)
  })
})

describe('FleetView split resizing', () => {
  it('clears the resize session when input is cancelled', async () => {
    await mountFleet('/dashboard')
    window.dispatchEvent(workspaceShortcut())
    await settle()

    const divider = document.querySelector<HTMLElement>(
      '[aria-label="Resize split view"]',
    )
    divider?.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
    expect(document.body.classList.contains('resizing-panes')).toBe(true)

    window.dispatchEvent(new PointerEvent('pointercancel'))

    expect(document.body.classList.contains('resizing-panes')).toBe(false)

    divider?.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
    expect(document.body.classList.contains('resizing-panes')).toBe(true)

    window.dispatchEvent(new Event('blur'))

    expect(document.body.classList.contains('resizing-panes')).toBe(false)
  })

  it('clears the resize session when the view unmounts', async () => {
    await mountFleet('/dashboard')
    window.dispatchEvent(workspaceShortcut())
    await settle()

    document
      .querySelector<HTMLElement>('[aria-label="Resize split view"]')
      ?.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }))
    expect(document.body.classList.contains('resizing-panes')).toBe(true)

    dispose()
    dispose = () => {}

    expect(document.body.classList.contains('resizing-panes')).toBe(false)
  })
})

describe('FleetView motion layout', () => {
  it('keeps the collapsed sidebar accessible and moves its highlight', async () => {
    const { host, router } = await mountAt('/dashboard')
    const toggle = host.querySelector<HTMLButtonElement>('.sidebar-toggle')
    expect(toggle?.classList).toContain('sidebar-toggle')

    toggle?.click()
    await settle()

    expect(host.querySelector('.shell')?.classList).toContain(
      'sidebar-collapsed',
    )
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    expect(host.querySelector<HTMLElement>('.sidebar')?.style.transform).toBe(
      '',
    )
    expect(
      host.querySelector<HTMLElement>('.main-shell')?.style.transform,
    ).toBe('')

    await router.push('/devices')
    await settle()

    expect(host.querySelectorAll('.nav-highlight')).toHaveLength(1)
    expect(
      host
        .querySelector('.nav-highlight')
        ?.closest('a')
        ?.getAttribute('aria-current'),
    ).toBe('page')

    expect(document.body.textContent).not.toContain('Expand sidebar')
    toggle?.dispatchEvent(new FocusEvent('focus', { bubbles: true }))
    await new Promise((resolve) => setTimeout(resolve, 400))
    await nextTick()
    expect(document.body.textContent).toContain('Expand sidebar')
  })
})

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
    expect(cell?.textContent).not.toContain('Mbit/s')
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
    const totals = host.querySelector('.page-heading p')
    expect(totals?.textContent).toContain('16 devices')
    expect(totals?.classList).toContain('tabular-nums')
    const headings = [...host.querySelectorAll('.dashboard h2')].map((h) =>
      h.textContent?.trim(),
    )
    expect(headings.slice(0, 2)).toEqual(['AI summary', 'Needs attention'])
  })

  it('keeps dashboard site, health, and device columns on phones', async () => {
    const { host } = await mountAt('/dashboard')
    const table = tableFor(host, 'sites-title')

    for (const label of ['Site', 'Health', 'Devices']) {
      const { head, cells } = column(table, label)
      expect(head.classList).not.toContain('max-[560px]:hidden')
      expect(
        cells.every((cell) => !cell.classList.contains('max-[560px]:hidden')),
      ).toBe(true)
    }
    for (const label of ['Clients', 'Traffic']) {
      const { head, cells } = column(table, label)
      expect(head.classList).toContain('max-[560px]:hidden')
      expect(
        cells.every((cell) => cell.classList.contains('max-[560px]:hidden')),
      ).toBe(true)
    }
  })

  it('keeps site, health, and the devices link on the phone Sites table', async () => {
    const { host } = await mountAt('/sites')
    const table = tableFor(host, 'sites-title')

    for (const label of ['Site', 'Health']) {
      const { head, cells } = column(table, label)
      expect(head.classList).not.toContain('max-[560px]:hidden')
      expect(
        cells.every((cell) => !cell.classList.contains('max-[560px]:hidden')),
      ).toBe(true)
    }
    for (const label of ['Open issue', 'Devices']) {
      const { head, cells } = column(table, label)
      expect(head.classList).toContain('max-[560px]:hidden')
      expect(
        cells.every((cell) => cell.classList.contains('max-[560px]:hidden')),
      ).toBe(true)
    }
    const linkColumn = table.querySelectorAll('th')[4]
    expect(linkColumn?.classList).not.toContain('max-[560px]:hidden')
    expect(table.querySelector('tbody td:nth-child(5)')?.textContent).toContain(
      'Devices',
    )
  })

  it('names event severity at the status text size', async () => {
    const { host } = await mountAt('/dashboard')
    const severity = [...host.querySelectorAll('span')].find((item) =>
      ['Critical', 'Warning', 'Info'].includes(item.textContent?.trim() ?? ''),
    )

    expect(severity?.classList).toContain('text-sm')
  })

  it('owns bug-button phone visibility in the component utility', async () => {
    const { host } = await mountAt('/dashboard')
    const button = host.querySelector('[aria-label="Report bug"]')

    expect(button?.classList).toContain('max-[560px]:hidden')
    expect(button?.classList).not.toContain('report-bug-button')
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
      new Intl.RelativeTimeFormat('en', {
        numeric: 'auto',
        style: 'short',
      }).format(-38, 'minute'),
    )
  })

  it('lets a phone device card announce its health, site, and age', async () => {
    const { host } = await mountAt('/devices?search=cologne-ap-02')
    const card = host.querySelector('.mobile-devices button')
    expect(card?.getAttribute('aria-label')).toBeNull()
    expect(card?.textContent).toContain('Offline')
    expect(card?.textContent).toContain('Cologne Central')
    expect(card?.textContent).toContain(
      new Intl.RelativeTimeFormat('en', {
        numeric: 'auto',
        style: 'short',
      }).format(-38, 'minute'),
    )
  })
})

describe('AI target coverage', () => {
  function listIds() {
    return registry.list().map((item) => item.id)
  }

  it('lists only the device-row copy the viewport shows', async () => {
    await mountAt('/devices')
    expect(listIds()).toContain('a:devices:device:desktop:dev-16')
    expect(listIds()).not.toContain('a:devices:device:mobile:dev-16')
    expect(
      registry.view('a:devices:device:desktop:dev-16')?.target.context,
    ).toMatchObject({ name: 'cologne-ap-02', health: 'Offline' })

    // A narrow viewport swaps the visible copy without remounting either.
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query.includes('reduce'),
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }))

    expect(listIds()).toContain('a:devices:device:mobile:dev-16')
    expect(listIds()).not.toContain('a:devices:device:desktop:dev-16')
  })

  it('drops a filtered-out target and restores it', async () => {
    const { router } = await mountAt('/devices')
    expect(registry.highlight('a:devices:device:desktop:dev-16')).toBe(true)

    await router.push({ path: '/devices', query: { search: 'berlin' } })
    await settle()
    expect(listIds()).not.toContain('a:devices:device:desktop:dev-16')
    expect(registry.highlight('a:devices:device:desktop:dev-16')).toBe(false)
    expect(registry.selection()).toBeUndefined()

    await router.push({ path: '/devices' })
    await settle()
    expect(listIds()).toContain('a:devices:device:desktop:dev-16')
    expect(registry.highlight('a:devices:device:desktop:dev-16')).toBe(true)
  })

  it('qualifies the same device by physical slot in each pane', async () => {
    await mountAt('/devices')
    window.dispatchEvent(workspaceShortcut())
    await settle()

    expect(listIds()).toContain('a:devices:device:desktop:dev-16')
    expect(listIds()).toContain('b:devices:device:desktop:dev-16')
  })

  it('registers the device view root and client rows with context', async () => {
    const { host } = await mountAt('/devices/dev-3')
    const root = registry.view('a:device:view:dev-3')
    expect(root).toBeDefined()
    expect(root?.target.context).toMatchObject({ site: 'Berlin Mitte' })
    expect(host.querySelector('.ai-ask')).toBeNull()

    const clientIds = listIds().filter((id) =>
      id.startsWith('a:device:client:'),
    )
    expect(clientIds.length).toBeGreaterThan(0)
  })

  it('updates the inventory view context with the active filters and results', async () => {
    const { router } = await mountAt('/devices')
    const id = 'a:devices:view:inventory'
    expect(registry.view(id)?.target.context).toMatchObject({
      search: 'none',
      status: 'all',
      total: '16',
      matching: '16',
    })

    await router.push({
      path: '/devices',
      query: { search: 'cologne', health: 'Offline' },
    })
    await settle()

    const filtered = registry.view(id)
    expect(filtered?.target.id).toBe(id)
    expect(filtered?.target.context).toMatchObject({
      search: 'cologne',
      status: 'Offline',
      matching: '1',
    })
  })

  it('registers the sites view root and its site rows with context', async () => {
    const { host } = await mountAt('/sites')
    const root = registry.view('a:sites:view:sites')
    expect(root?.target.context).toMatchObject({ sites: '4' })
    expect(root?.element).toBe(
      host.querySelector('#sites-title')?.closest('section'),
    )

    const rows = listIds().filter((id) => id.startsWith('a:sites:site:'))
    expect(rows.length).toBe(4)
  })

  it('registers the dashboard view root and its site rows with context', async () => {
    await mountAt('/dashboard')
    const root = registry.view('a:dashboard:view:all')
    expect(root?.target.label).toBe('Dashboard · all sites')
    expect(root?.target.context).toMatchObject({ devices: '16' })

    const berlin = registry.view('a:dashboard:site:berlin')
    expect(berlin?.target.kind).toBe('site')
    expect(berlin?.target.context).toMatchObject({ name: 'Berlin Mitte' })
  })

  it('registers the clients view root and client rows with context', async () => {
    await mountAt('/clients')
    const root = registry.view('a:clients:view:all')
    expect(root?.target.context).toMatchObject({
      accessPoint: 'All access points',
      search: 'none',
    })

    const clients = listIds().filter((id) => id.startsWith('a:clients:client:'))
    expect(clients.length).toBeGreaterThan(0)
    expect(registry.view(clients[0] ?? '')?.target.context.mac).toMatch(
      /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/,
    )
  })

  it('updates the clients view search context without changing its id', async () => {
    const { router } = await mountAt('/clients')
    await router.push({ path: '/clients', query: { q: 'printer' } })
    await settle()

    expect(registry.view('a:clients:view:all')?.target.context).toMatchObject({
      search: 'printer',
    })
  })

  it('updates the clients view context when the band filter changes', async () => {
    const { host } = await mountAt('/clients')
    const id = 'a:clients:view:all'
    const before = registry.view(id)?.target.context
    expect(before).toMatchObject({ band: 'all', matching: before?.count })

    const trigger = host.querySelector<HTMLButtonElement>(
      '[aria-label="Filter by band"]',
    )
    expect(trigger).not.toBeNull()
    trigger?.dispatchEvent(
      new PointerEvent('pointerdown', {
        bubbles: true,
        cancelable: true,
        button: 0,
      }),
    )
    trigger?.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }),
    )
    await settle()
    const option = [
      ...document.querySelectorAll<HTMLElement>('[role="option"]'),
    ].find((item) => item.textContent?.includes('2.4 GHz'))
    expect(option).toBeDefined()
    option?.focus()
    option?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
        cancelable: true,
      }),
    )
    await settle()

    const after = registry.view(id)?.target.context
    expect(after?.band).toBe('2.4 GHz')
    expect(Number(after?.matching)).toBeLessThan(Number(after?.count))
    expect(registry.view(id)?.target.id).toBe(id)
  })

  it('qualifies a view root by physical slot in each pane', async () => {
    await mountAt('/sites')
    window.dispatchEvent(workspaceShortcut())
    await settle()

    expect(listIds()).toContain('a:sites:view:sites')
    expect(listIds()).toContain('b:sites:view:sites')
  })
})
