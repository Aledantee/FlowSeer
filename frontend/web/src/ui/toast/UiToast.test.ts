// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import { ToastProvider, ToastViewport } from 'reka-ui'
import UiToast from './UiToast.vue'
import UiToastProvider from './UiToastProvider.vue'
import { useToast } from './useToast'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  const cleanup = dispose
  dispose = () => {}
  try {
    cleanup()
  } catch {
    // ignore unmount failure
  }
  const { toasts } = useToast()
  toasts.value = []
  document.body.replaceChildren()
})

function mountApp(
  renderFn: () => unknown,
  localeOrI18n: WebLocale | ReturnType<typeof createWebI18n> = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: renderFn,
  })
  app.config.errorHandler = (err) => {
    throw err
  }
  if (typeof localeOrI18n === 'string') {
    app.use(createWebI18n(localeOrI18n))
  } else {
    app.use(localeOrI18n)
  }
  dispose = () => app.unmount()
  app.mount(host)
  return host
}

describe('UiToast', () => {
  it('dispatched toast renders title, description, and variant border styling', async () => {
    mountApp(() => h(UiToastProvider))

    const { toast } = useToast()
    toast({
      title: 'Success Title',
      description: 'Operation completed successfully.',
      variant: 'success',
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.textContent).toContain('Success Title')
    expect(document.body.textContent).toContain(
      'Operation completed successfully.',
    )

    const toastEl = document.body.querySelector('ol li') as HTMLElement
    expect(toastEl).not.toBeNull()
    expect(toastEl.className).toContain('border-success-border')
  })

  it('action button click invokes callback', async () => {
    mountApp(() => h(UiToastProvider))

    let actionInvoked = false
    const { toast } = useToast()
    toast({
      title: 'Action Toast',
      action: {
        label: 'Undo Action',
        onClick: () => {
          actionInvoked = true
        },
      },
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const actionBtn = Array.from(document.body.querySelectorAll('button')).find(
      (btn) => btn.textContent?.includes('Undo Action'),
    )
    expect(actionBtn).toBeDefined()
    actionBtn?.click()

    expect(actionInvoked).toBe(true)
  })

  it('toast auto-dismisses after duration', async () => {
    mountApp(() => h(UiToastProvider))

    const { toast } = useToast()
    toast({
      title: 'Dismissable Toast',
      duration: 100,
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(document.body.textContent).toContain('Dismissable Toast')

    await new Promise((r) => setTimeout(r, 120))
    await nextTick()

    expect(document.body.querySelector('ol li')).toBeNull()
  })

  it('provider duration governs dispatched toasts that omit duration', async () => {
    mountApp(() => h(UiToastProvider, { duration: 100 }))

    const { toast } = useToast()
    toast({
      title: 'Provider Duration Toast',
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(document.body.textContent).toContain('Provider Duration Toast')

    await new Promise((r) => setTimeout(r, 120))
    await nextTick()

    expect(document.body.querySelector('ol li')).toBeNull()
  })

  it('per-toast duration overrides provider duration', async () => {
    mountApp(() => h(UiToastProvider, { duration: 600 }))

    const { toast } = useToast()
    toast({
      title: 'Per Toast Override',
      duration: 100,
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))
    expect(document.body.textContent).toContain('Per Toast Override')

    await new Promise((r) => setTimeout(r, 120))
    await nextTick()

    expect(document.body.querySelector('ol li')).toBeNull()
  })

  it('removes dismissed toast from store on the close render when computed animationName is none', async () => {
    mountApp(() => h(UiToastProvider))

    const { toast, dismiss, toasts } = useToast()
    const id = toast({
      title: 'Immediate Dismiss',
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(toasts.value.some((t) => t.id === id)).toBe(true)

    dismiss(id)
    await nextTick()

    expect(toasts.value.some((t) => t.id === id)).toBe(false)
  })

  it('removes toast from store in the same tick when close button is clicked and animationName is none', async () => {
    mountApp(() => h(UiToastProvider))

    const { toast, toasts } = useToast()
    const id = toast({
      title: 'Close Button Dismiss',
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(toasts.value.some((t) => t.id === id)).toBe(true)

    const closeBtn = document.body.querySelector(
      'button[aria-label="Close"]',
    ) as HTMLElement
    expect(closeBtn).not.toBeNull()
    closeBtn.click()

    expect(toasts.value.some((t) => t.id === id)).toBe(false)
  })

  it('delays toast removal from store until animationend when animationName is fade-out', async () => {
    const originalGetComputedStyle = window.getComputedStyle
    window.getComputedStyle = (elt: Element, pseudoElt?: string | null) => {
      const style = originalGetComputedStyle(elt, pseudoElt)
      const state = elt.getAttribute('data-state')
      let animationName = 'none'
      if (
        state === 'closed' &&
        elt.classList.contains('data-[state=closed]:animate-fade-out')
      ) {
        animationName = 'fade-out'
      } else if (
        state === 'open' &&
        elt.classList.contains('data-[state=open]:animate-fade-in')
      ) {
        animationName = 'fade-in'
      }
      return new Proxy(style, {
        get(target, prop, receiver) {
          if (prop === 'animationName') {
            return animationName
          }
          return Reflect.get(target, prop, receiver)
        },
      })
    }

    try {
      mountApp(() => h(UiToastProvider))

      const { toast, dismiss, toasts } = useToast()
      const id = toast({
        title: 'Animated Toast',
      })

      await nextTick()
      await new Promise((r) => setTimeout(r, 20))

      const toastEl = document.body.querySelector('ol li') as HTMLElement
      expect(toastEl).not.toBeNull()

      dismiss(id)
      await nextTick()
      await new Promise((r) => setTimeout(r, 20))

      expect(toastEl.isConnected).toBe(true)
      expect(toasts.value.some((t) => t.id === id)).toBe(true)

      toastEl.firstElementChild?.dispatchEvent(
        new AnimationEvent('animationend', {
          animationName: 'fade-out',
          bubbles: true,
        }),
      )
      await nextTick()

      expect(toasts.value.some((t) => t.id === id)).toBe(true)

      toastEl.dispatchEvent(
        new AnimationEvent('animationend', {
          animationName: 'fade-out',
          bubbles: true,
        }),
      )
      await nextTick()

      expect(toasts.value.some((t) => t.id === id)).toBe(false)
    } finally {
      window.getComputedStyle = originalGetComputedStyle
    }
  })

  it('a reopened standalone toast emits closed again on its next close', async () => {
    const open = ref(true)
    let closedCount = 0
    mountApp(() =>
      h(ToastProvider, null, () => [
        h(UiToast, {
          title: 'Standalone',
          open: open.value,
          'onUpdate:open': (val: boolean) => {
            open.value = val
          },
          onClosed: () => {
            closedCount += 1
          },
        }),
        h(ToastViewport),
      ]),
    )
    await nextTick()

    open.value = false
    await nextTick()
    expect(closedCount).toBe(1)

    open.value = true
    await nextTick()
    open.value = false
    await nextTick()
    expect(closedCount).toBe(2)
  })

  it('renders English default accessible names for viewport, close button, and announcement prefix', async () => {
    mountApp(() => h(UiToastProvider), 'en')

    const viewport = document.body.querySelector('div[role="region"]')
    expect(viewport?.getAttribute('aria-label')).toBe('Notifications (F8)')

    const { toast } = useToast()
    toast({
      title: 'English Toast',
      description: 'Operation completed successfully.',
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const closeBtn = document.body.querySelector('button[aria-label="Close"]')
    expect(closeBtn).not.toBeNull()

    expect(document.body.textContent).toContain('Notification ')
  })

  it('asserts German accessible names through the DOM and German announcement prefix', async () => {
    mountApp(() => h(UiToastProvider), 'de')

    const viewport = document.body.querySelector('div[role="region"]')
    expect(viewport?.getAttribute('aria-label')).toBe('Benachrichtigungen (F8)')

    const { toast } = useToast()
    toast({
      title: 'German Toast',
      description: 'Vorgang erfolgreich abgeschlossen.',
      action: {
        label: 'Aktion',
        onClick: () => {},
      },
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const closeBtn = document.body.querySelector(
      'button[aria-label="Schließen"]',
    )
    expect(closeBtn).not.toBeNull()

    expect(document.body.textContent).toContain('Benachrichtigung ')
  })

  it('preserves explicit announcementLabel and viewportLabel overrides on UiToastProvider', async () => {
    mountApp(
      () =>
        h(UiToastProvider, {
          announcementLabel: 'System Alert',
          viewportLabel: 'System Notifications ({hotkey})',
        }),
      'de',
    )

    const viewport = document.body.querySelector('div[role="region"]')
    expect(viewport?.getAttribute('aria-label')).toBe(
      'System Notifications (F8)',
    )

    const { toast } = useToast()
    toast({
      title: 'Alert Title',
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.textContent).toContain('System Alert ')

    dispose()
    document.body.replaceChildren()

    mountApp(
      () =>
        h(UiToastProvider, {
          viewportLabel: (hotkey: string) => `Overlay: ${hotkey}`,
        }),
      'de',
    )

    const funcViewport = document.body.querySelector('div[role="region"]')
    expect(funcViewport?.getAttribute('aria-label')).toBe('Overlay: F8')
  })

  it('preserves explicit closeLabel and actionAltText overrides on UiToast', async () => {
    mountApp(
      () =>
        h(ToastProvider, null, () => [
          h(UiToast, {
            open: true,
            title: 'Custom Labels',
            actionText: 'Retry',
            actionAltText: 'Retry Operation',
            closeLabel: 'Dismiss Alert',
          }),
          h(ToastViewport),
        ]),
      'de',
    )
    await nextTick()

    const closeBtn = document.body.querySelector(
      'button[aria-label="Dismiss Alert"]',
    )
    expect(closeBtn).not.toBeNull()

    dispose()
    document.body.replaceChildren()

    mountApp(
      () =>
        h(ToastProvider, null, () => [
          h(UiToast, {
            open: true,
            title: 'Empty Close Label',
            closeLabel: '',
          }),
          h(ToastViewport),
        ]),
      'de',
    )
    await nextTick()

    const emptyCloseBtn = document.body.querySelector('button[aria-label=""]')
    expect(emptyCloseBtn).not.toBeNull()
  })

  it('retains explicit overrides after changing locale', async () => {
    const i18n = createWebI18n('en')
    mountApp(
      () =>
        h(UiToastProvider, {
          viewportLabel: 'Persistent ({hotkey})',
          announcementLabel: 'Persistent Notice',
        }),
      i18n,
    )

    const viewport = document.body.querySelector('div[role="region"]')
    expect(viewport?.getAttribute('aria-label')).toBe('Persistent (F8)')

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(viewport?.getAttribute('aria-label')).toBe('Persistent (F8)')
  })

  it("asserts Reka rejects actionAltText: '' for an action", async () => {
    let capturedError: unknown = null
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render: () =>
        h(ToastProvider, null, () => [
          h(UiToast, {
            open: true,
            actionText: 'Action',
            actionAltText: '',
          }),
          h(ToastViewport),
        ]),
    })
    app.config.errorHandler = (err) => {
      capturedError = err
    }
    app.use(createWebI18n())
    app.mount(host)
    dispose = () => app.unmount()

    await nextTick()
    expect(capturedError).toBeInstanceOf(Error)
    expect((capturedError as Error).message).toMatch(/altText/i)
  })

  it('asserts Reka rejects a whitespace-only announcementLabel', () => {
    expect(() =>
      mountApp(() =>
        h(UiToastProvider, {
          announcementLabel: '   ',
        }),
      ),
    ).toThrow(/non-empty `string`/i)
  })
})
