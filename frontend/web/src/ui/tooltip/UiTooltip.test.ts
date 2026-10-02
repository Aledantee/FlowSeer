// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { TooltipProvider } from 'reka-ui'
import UiTooltip from './UiTooltip.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
})

function mountTooltip(
  props: Record<string, unknown> = {},
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(TooltipProvider, {}, () =>
        h(
          UiTooltip,
          {
            label: 'Quick action',
            hint: 'Shortcut hint',
            delayDuration: 0,
            ...props,
          },
          {
            default: () =>
              h('button', { class: 'custom-target-button' }, 'Target Button'),
          },
        ),
      )
    },
  })
  const i18n = createWebI18n(locale)
  app.use(i18n)
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const trigger = host.querySelector('button.custom-target-button')
  if (!trigger) throw new Error('Missing trigger button')
  return { host, trigger: trigger as HTMLButtonElement, i18n }
}

async function settle() {
  await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 50))
  await nextTick()
}

function accessibleText(): string | null | undefined {
  return document.body.querySelector('[role="tooltip"]')?.textContent
}

function keyTexts(): (string | null)[] {
  return Array.from(document.body.querySelectorAll('kbd')).map(
    (kbd) => kbd.textContent,
  )
}

describe('UiTooltip', () => {
  it('renders trigger child directly and displays content on hover', async () => {
    const { trigger } = mountTooltip()
    expect(trigger.textContent).toBe('Target Button')

    trigger.dispatchEvent(
      new PointerEvent('pointermove', {
        bubbles: true,
        cancelable: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    const content = document.body.textContent
    expect(content).toContain('Quick action')
    expect(content).toContain('Shortcut hint')
  })

  it('displays content on keyboard focus and fails if focus handling is absent', async () => {
    const { trigger } = mountTooltip()

    expect(document.body.textContent).not.toContain('Quick action')
    expect(document.body.textContent).not.toContain('Shortcut hint')

    trigger.dispatchEvent(
      new FocusEvent('focus', {
        bubbles: true,
      }),
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(document.body.textContent).toContain('Quick action')
    expect(document.body.textContent).toContain('Shortcut hint')
  })

  it('translates Ctrl to Strg for a non-Mac Shortcut in German', async () => {
    mountTooltip(
      {
        shortcut: { code: 'KeyK', mod: true },
        defaultOpen: true,
      },
      'de',
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    const content = document.body.textContent
    expect(content).toContain('Strg')
    expect(content).toContain('K')
  })

  it('renders Mac glyphs for Mac shortcuts', async () => {
    const originalPlatform = navigator.platform
    Object.defineProperty(navigator, 'platform', {
      value: 'MacIntel',
      configurable: true,
    })
    try {
      mountTooltip(
        {
          shortcut: { code: 'KeyK', mod: true, alt: true },
          defaultOpen: true,
        },
        'de',
      )
      await nextTick()
      await new Promise((resolve) => setTimeout(resolve, 50))
      await nextTick()

      const content = document.body.textContent
      expect(content).toContain('⌥')
      expect(content).toContain('⌘')
      expect(content).toContain('K')
    } finally {
      Object.defineProperty(navigator, 'platform', {
        value: originalPlatform,
        configurable: true,
      })
    }
  })

  it('keeps explicit string arrays as caller text without translation', async () => {
    mountTooltip(
      {
        shortcut: ['Ctrl', 'K'],
        defaultOpen: true,
      },
      'de',
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    const content = document.body.textContent
    expect(content).toContain('Ctrl')
    expect(content).not.toContain('Strg')
  })

  it('supports an explicit keyLabel override', async () => {
    mountTooltip(
      {
        shortcut: { code: 'KeyK', mod: true },
        keyLabel: (key: string) => (key === 'Ctrl' ? 'Steuerung' : key),
        defaultOpen: true,
      },
      'de',
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    const content = document.body.textContent
    expect(content).toContain('Steuerung')
    expect(content).not.toContain('Strg')
  })

  it('updates translated shortcut keys on locale switch while open', async () => {
    const { i18n } = mountTooltip(
      {
        shortcut: { code: 'KeyK', mod: true },
        defaultOpen: true,
      },
      'en',
    )
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(document.body.textContent).toContain('Ctrl')
    expect(document.body.textContent).not.toContain('Strg')

    i18n.global.locale.value = 'de'
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 50))
    await nextTick()

    expect(document.body.textContent).toContain('Strg')
    expect(document.body.textContent).not.toContain('Ctrl')
    expect(accessibleText()).toBe('Quick action Shortcut hint Strg K')
  })

  it('names the tooltip with label, hint, and keys separated by single spaces in en and de', async () => {
    const cases: { locale: WebLocale; name: string; keys: string[] }[] = [
      {
        locale: 'en',
        name: 'Quick action Shortcut hint Ctrl Shift K',
        keys: ['Ctrl', 'Shift', 'K'],
      },
      {
        locale: 'de',
        name: 'Quick action Shortcut hint Strg Umschalt K',
        keys: ['Strg', 'Umschalt', 'K'],
      },
    ]
    for (const { locale, name, keys } of cases) {
      mountTooltip(
        {
          shortcut: { code: 'KeyK', mod: true, shift: true },
          defaultOpen: true,
        },
        locale,
      )
      await settle()

      expect(accessibleText()).toBe(name)
      expect(keyTexts()).toEqual(keys)
      dispose()
    }
  })

  it('names the tooltip with label and hint alone without a shortcut or with an empty one', async () => {
    for (const shortcut of [undefined, [] as string[]]) {
      mountTooltip({ shortcut, defaultOpen: true })
      await settle()

      expect(accessibleText()).toBe('Quick action Shortcut hint')
      expect(keyTexts()).toEqual([])
      dispose()
    }
  })

  it('translates every non-Mac word key in de and keeps the key names in en', async () => {
    const shortcut = { code: 'Escape', mod: true, alt: true, shift: true }
    const cases: { locale: WebLocale; keys: string[] }[] = [
      { locale: 'en', keys: ['Ctrl', 'Alt', 'Shift', 'Esc'] },
      { locale: 'de', keys: ['Strg', 'Alt', 'Umschalt', 'Esc'] },
    ]
    for (const { locale, keys } of cases) {
      mountTooltip({ shortcut, defaultOpen: true }, locale)
      await settle()

      expect(keyTexts()).toEqual(keys)
      expect(accessibleText()).toBe(
        `Quick action Shortcut hint ${keys.join(' ')}`,
      )
      dispose()
    }
  })

  it('shows a key code that names an Object.prototype member as is', async () => {
    mountTooltip(
      { shortcut: { code: 'Keyconstructor' }, defaultOpen: true },
      'de',
    )
    await settle()

    expect(keyTexts()).toEqual(['constructor'])
    expect(accessibleText()).toBe('Quick action Shortcut hint constructor')
  })
})
