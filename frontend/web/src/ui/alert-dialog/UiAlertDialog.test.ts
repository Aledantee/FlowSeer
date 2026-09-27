// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiAlertDialog, { type UiAlertDialogProps } from './UiAlertDialog.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountAlertDialog(
  props: Partial<UiAlertDialogProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiAlertDialog, props as UiAlertDialogProps, slots)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiAlertDialog', () => {
  it('renders with role="alertdialog"', async () => {
    mountAlertDialog({
      title: 'Alert Title',
      description: 'Alert Description',
      defaultOpen: true,
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="alertdialog"]')
    expect(dialog).not.toBeNull()
    expect(document.body.textContent).toContain('Alert Title')
    expect(document.body.textContent).toContain('Alert Description')
  })

  it('clicking overlay scrim does not close dialog', async () => {
    const isOpen = ref(true)
    mountAlertDialog({
      title: 'Critical Warning',
      description: 'Cannot be dismissed by outside click.',
      open: isOpen.value,
      'onUpdate:open': (v: boolean) => {
        isOpen.value = v
      },
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const overlay = document.body.querySelector('.bg-overlay') as HTMLElement
    expect(overlay).not.toBeNull()

    overlay.dispatchEvent(
      new PointerEvent('pointerdown', {
        bubbles: true,
        cancelable: true,
      }),
    )
    overlay.click()

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(isOpen.value).toBe(true)
    expect(document.body.querySelector('[role="alertdialog"]')).not.toBeNull()
  })

  it('action button emits confirm and cancel button emits cancel', async () => {
    let confirmed = false
    let cancelled = false
    const isOpen = ref(true)

    mountAlertDialog({
      title: 'Confirm Action',
      description: 'Are you sure?',
      confirmText: 'Do it',
      cancelText: 'Abort',
      destructive: true,
      open: isOpen.value,
      'onUpdate:open': (v: boolean) => {
        isOpen.value = v
      },
      onConfirm: () => {
        confirmed = true
      },
      onCancel: () => {
        cancelled = true
      },
    })

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const buttons = document.body.querySelectorAll('button')
    const cancelButton = Array.from(buttons).find(
      (b) => b.textContent?.trim() === 'Abort',
    )
    const actionButton = Array.from(buttons).find(
      (b) => b.textContent?.trim() === 'Do it',
    )

    expect(cancelButton).toBeDefined()
    expect(actionButton).toBeDefined()

    cancelButton?.click()
    await nextTick()
    expect(cancelled).toBe(true)

    actionButton?.click()
    await nextTick()
    expect(confirmed).toBe(true)
  })
})
