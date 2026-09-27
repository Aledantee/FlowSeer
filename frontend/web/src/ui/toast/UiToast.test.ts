// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
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
})
