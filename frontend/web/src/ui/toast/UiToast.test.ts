// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import { ToastProvider, ToastViewport } from 'reka-ui'
import UiToast from './UiToast.vue'
import UiToastProvider from './UiToastProvider.vue'
import { useToast } from './useToast'

let dispose = () => {}
afterEach(() => {
  dispose()
  const { toasts } = useToast()
  toasts.value = []
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
})
