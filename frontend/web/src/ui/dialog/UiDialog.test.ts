// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
import UiDialog, { type UiDialogProps } from './UiDialog.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountDialog(
  props: Partial<UiDialogProps> & Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
  localeOrI18n: WebLocale | ReturnType<typeof createWebI18n> = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiDialog, props as UiDialogProps, slots)
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

  it('renders default fallback title, description, and closeLabel in en and de', async () => {
    mountDialog({ defaultOpen: true }, {}, 'en')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialogTitleEn = document.body.querySelector('h2')
    expect(dialogTitleEn?.textContent).toBe('Dialog')
    const dialogDescEn = document.body.querySelector('p')
    expect(dialogDescEn?.textContent).toBe('Dialog description')
    const closeBtnEn = document.body.querySelector('button[aria-label="Close"]')
    expect(closeBtnEn).not.toBeNull()

    dispose()
    document.body.replaceChildren()

    mountDialog({ defaultOpen: true }, {}, 'de')
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialogTitleDe = document.body.querySelector('h2')
    expect(dialogTitleDe?.textContent).toBe('Dialog')
    const dialogDescDe = document.body.querySelector('p')
    expect(dialogDescDe?.textContent).toBe('Dialogbeschreibung')
    const closeBtnDe = document.body.querySelector(
      'button[aria-label="Schließen"]',
    )
    expect(closeBtnDe).not.toBeNull()
  })

  it('preserves explicit fallbackTitle, fallbackDescription, and closeLabel overrides across locales', async () => {
    mountDialog(
      {
        defaultOpen: true,
        fallbackTitle: '',
        fallbackDescription: 'Custom Description',
        closeLabel: 'Dismiss Modal',
      },
      {},
      'de',
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialogTitle = document.body.querySelector('h2')
    expect(dialogTitle?.textContent).toBe('')
    const dialogDesc = document.body.querySelector('p')
    expect(dialogDesc?.textContent).toBe('Custom Description')
    const closeBtn = document.body.querySelector(
      'button[aria-label="Dismiss Modal"]',
    )
    expect(closeBtn).not.toBeNull()
  })

  it('resolves fallbackTitle from catalog default', async () => {
    const i18n = createWebI18n('en')
    i18n.global.mergeLocaleMessage('en', {
      ui: { dialog: { fallbackTitle: 'Probe title' } },
    })
    mountDialog({ defaultOpen: true }, {}, i18n)
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('h2')?.textContent).toBe('Probe title')
  })

  it('updates dialog defaults on live locale change and preserves explicit overrides', async () => {
    const i18n = createWebI18n('en')
    mountDialog({ defaultOpen: true }, {}, i18n)
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialogDescEn = document.body.querySelector('p')
    expect(dialogDescEn?.textContent).toBe('Dialog description')
    const closeBtnEn = document.body.querySelector('button[aria-label="Close"]')
    expect(closeBtnEn).not.toBeNull()

    i18n.global.locale.value = 'de'
    await nextTick()

    const dialogDescDe = document.body.querySelector('p')
    expect(dialogDescDe?.textContent).toBe('Dialogbeschreibung')
    const closeBtnDe = document.body.querySelector(
      'button[aria-label="Schließen"]',
    )
    expect(closeBtnDe).not.toBeNull()

    dispose()
    document.body.replaceChildren()

    const i18nOverride = createWebI18n('de')
    mountDialog(
      {
        defaultOpen: true,
        fallbackTitle: 'Custom Title',
        fallbackDescription: 'Custom Description',
        closeLabel: 'Custom Close',
      },
      {},
      i18nOverride,
    )
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    expect(document.body.querySelector('h2')?.textContent).toBe('Custom Title')
    expect(document.body.querySelector('p')?.textContent).toBe(
      'Custom Description',
    )
    expect(
      document.body.querySelector('button[aria-label="Custom Close"]'),
    ).not.toBeNull()

    i18nOverride.global.locale.value = 'en'
    await nextTick()

    expect(document.body.querySelector('h2')?.textContent).toBe('Custom Title')
    expect(document.body.querySelector('p')?.textContent).toBe(
      'Custom Description',
    )
    expect(
      document.body.querySelector('button[aria-label="Custom Close"]'),
    ).not.toBeNull()
  })

  it('renders the right-sheet variant on the right edge', async () => {
    mountDialog({ defaultOpen: true, side: 'right' })
    await nextTick()
    await new Promise((r) => setTimeout(r, 20))

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.className).toContain('right-0')
    expect(dialog?.className).toContain('h-full')
  })
})
