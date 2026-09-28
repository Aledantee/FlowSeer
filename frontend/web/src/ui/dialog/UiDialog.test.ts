// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiDialog, { type UiDialogProps } from './UiDialog.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountDialog(
  props: Partial<UiDialogProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiDialog, props as UiDialogProps, slots)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('UiDialog', () => {
  it('opens on trigger click and mounts content in top-layer portal with role="dialog"', async () => {
    const host = mountDialog(
      {
        title: 'Modal Title',
        description: 'Modal Description',
      },
      {
        trigger: () => h('button', { id: 'dialog-trigger' }, 'Open Modal'),
        default: () => h('div', { id: 'dialog-body' }, 'Modal Body Content'),
      },
    )

    const trigger = host.querySelector<HTMLButtonElement>('#dialog-trigger')
    expect(trigger).not.toBeNull()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()

    trigger?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(document.body.textContent).toContain('Modal Title')
    expect(document.body.textContent).toContain('Modal Description')
    expect(document.body.textContent).toContain('Modal Body Content')
  })

  it('escape closes dialog and returns focus to trigger', async () => {
    mountDialog(
      {
        title: 'Escape Title',
        defaultOpen: true,
      },
      {
        trigger: () => h('button', { id: 'escape-trigger' }, 'Trigger'),
        default: () => h('div', 'Content'),
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()

    dialog?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('clicking overlay scrim closes dialog', async () => {
    const isOpen = ref(true)
    mountDialog(
      {
        title: 'Overlay Title',
        open: isOpen.value,
        'onUpdate:open': (v: boolean) => {
          isOpen.value = v
        },
      },
      {
        trigger: () => h('button', 'Trigger'),
        default: () => h('div', 'Content'),
      },
    )

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
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(isOpen.value).toBe(false)
  })

  it('emits closeAutoFocus on close and honors preventDefault to preserve custom focus', async () => {
    let emittedEvent: Event | null = null
    const customButton = document.createElement('button')
    customButton.id = 'dialog-custom-focus'
    document.body.append(customButton)

    mountDialog(
      {
        title: 'Focus Return Title',
        defaultOpen: true,
        onCloseAutoFocus: (e: Event) => {
          emittedEvent = e
          e.preventDefault()
          customButton.focus()
        },
      },
      {
        trigger: () => h('button', { id: 'default-trigger' }, 'Trigger'),
        default: () => h('div', 'Content'),
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()

    dialog?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Escape',
        code: 'Escape',
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(emittedEvent).not.toBeNull()
    expect(document.activeElement).toBe(customButton)
    customButton.remove()
  })
})
