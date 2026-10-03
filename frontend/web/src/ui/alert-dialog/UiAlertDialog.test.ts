// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiAlertDialog, { type UiAlertDialogProps } from './UiAlertDialog.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountAlertDialog(
  props: Partial<UiAlertDialogProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
  localeOrI18n: WebLocale | ReturnType<typeof createWebI18n> = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiAlertDialog, props as UiAlertDialogProps, slots)
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

  it('emits closeAutoFocus on close and honors preventDefault to preserve custom focus', async () => {
    let emittedEvent: Event | null = null
    const customButton = document.createElement('button')
    customButton.id = 'alert-custom-focus'
    document.body.append(customButton)

    mountAlertDialog(
      {
        title: 'Alert Focus Return',
        description: 'Description',
        defaultOpen: true,
        onCloseAutoFocus: (e: Event) => {
          emittedEvent = e
          e.preventDefault()
          customButton.focus()
        },
      },
      {
        trigger: () => h('button', { id: 'alert-default-trigger' }, 'Trigger'),
      },
    )

    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="alertdialog"]')
    expect(dialog).not.toBeNull()

    const cancelButton = Array.from(
      document.body.querySelectorAll('button'),
    ).find((b) => b.textContent?.trim() === 'Cancel')
    cancelButton?.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(emittedEvent).not.toBeNull()
    expect(document.activeElement).toBe(customButton)
    customButton.remove()
  })

  it('renders default confirm and cancel button text in en and de', async () => {
    mountAlertDialog(
      {
        title: 'Default Action',
        description: 'Testing default labels',
        defaultOpen: true,
      },
      {},
      'en',
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const buttonsEn = Array.from(document.body.querySelectorAll('button'))
    expect(buttonsEn.some((b) => b.textContent?.trim() === 'Confirm')).toBe(
      true,
    )
    expect(buttonsEn.some((b) => b.textContent?.trim() === 'Cancel')).toBe(true)

    dispose()
    document.body.replaceChildren()

    mountAlertDialog(
      {
        title: 'Standardaktion',
        description: 'Test der Standardbeschriftungen',
        defaultOpen: true,
      },
      {},
      'de',
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const buttonsDe = Array.from(document.body.querySelectorAll('button'))
    expect(buttonsDe.some((b) => b.textContent?.trim() === 'Bestätigen')).toBe(
      true,
    )
    expect(buttonsDe.some((b) => b.textContent?.trim() === 'Abbrechen')).toBe(
      true,
    )
  })

  it('preserves explicit confirmText and cancelText overrides across locales, including empty strings', async () => {
    mountAlertDialog(
      {
        title: 'Custom Action',
        description: 'Testing overrides',
        confirmText: 'Proceed',
        cancelText: '',
        defaultOpen: true,
      },
      {},
      'de',
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const buttons = Array.from(document.body.querySelectorAll('button'))
    expect(buttons.some((b) => b.textContent?.trim() === 'Proceed')).toBe(true)
    expect(buttons.some((b) => b.textContent?.trim() === '')).toBe(true)
    expect(buttons.some((b) => b.textContent?.trim() === 'Bestätigen')).toBe(
      false,
    )
    expect(buttons.some((b) => b.textContent?.trim() === 'Abbrechen')).toBe(
      false,
    )
  })

  it('updates default confirm and cancel button labels on live locale change and preserves explicit overrides', async () => {
    const i18n = createWebI18n('en')
    mountAlertDialog(
      {
        title: 'Title',
        description: 'Description',
        defaultOpen: true,
      },
      {},
      i18n,
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const buttonsEn = Array.from(document.body.querySelectorAll('button'))
    const cancelEn = buttonsEn.find((b) => b.textContent?.trim() === 'Cancel')
    const confirmEn = buttonsEn.find((b) => b.textContent?.trim() === 'Confirm')
    expect(cancelEn).toBeDefined()
    expect(confirmEn).toBeDefined()

    i18n.global.locale.value = 'de'
    await nextTick()

    const buttonsDe = Array.from(document.body.querySelectorAll('button'))
    const cancelDe = buttonsDe.find(
      (b) => b.textContent?.trim() === 'Abbrechen',
    )
    const confirmDe = buttonsDe.find(
      (b) => b.textContent?.trim() === 'Bestätigen',
    )
    expect(cancelDe).toBeDefined()
    expect(confirmDe).toBeDefined()

    dispose()
    document.body.replaceChildren()

    mountAlertDialog(
      {
        title: 'Title',
        description: 'Description',
        confirmText: 'Keep Confirm',
        cancelText: 'Keep Cancel',
        defaultOpen: true,
      },
      {},
      i18n,
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.textContent).toContain('Keep Confirm')
    expect(document.body.textContent).toContain('Keep Cancel')

    i18n.global.locale.value = 'en'
    await nextTick()

    expect(document.body.textContent).toContain('Keep Confirm')
    expect(document.body.textContent).toContain('Keep Cancel')
  })
})
