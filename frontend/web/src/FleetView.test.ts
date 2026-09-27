// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { isMac } from './navigation/shortcuts'

let dispose = () => {}

// happy-dom has no Web Animations API, so the view runs as it does for a
// user who asked for reduced motion.
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

afterEach(() => {
  dispose()
  dispose = () => {}
  localStorage.clear()
  sessionStorage.clear()
  document.body.replaceChildren()
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
  const app = createApp(FleetView)
  await router.push(path)
  app.use(router)
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
