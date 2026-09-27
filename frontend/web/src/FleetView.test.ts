// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import FleetView from './FleetView.vue'
import { isMac } from './navigation/shortcuts'

let dispose = () => {}

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({
      matches: true,
      media: '',
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

afterEach(() => {
  dispose()
  dispose = () => {}
  localStorage.clear()
  sessionStorage.clear()
  document.body.replaceChildren()
})

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 20))
}

async function mountFleet(path: string) {
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
