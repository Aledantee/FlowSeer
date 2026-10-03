// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { UiAppRoot } from '../ui'
import LocaleSwitcher from './LocaleSwitcher.vue'
import { createWebI18n } from '../i18n'
import type { WebLocale } from '../i18n'
import { i18nWarnings } from '../i18n/testing'

let dispose = () => {}

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
  localStorage.clear()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function mountSwitcher(locale: WebLocale = 'en') {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiAppRoot, {}, () => h(LocaleSwitcher))
    },
  })
  const i18n = createWebI18n(locale)
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()
  const button = host.querySelector<HTMLButtonElement>('button.locale-switcher')
  if (!button) throw new Error('Missing locale switcher button')
  return {
    host,
    i18n,
    button,
    status: () => host.querySelector('[role="status"]')?.textContent?.trim(),
  }
}

describe('LocaleSwitcher', () => {
  it('shows the active code and names the other language in its own language', () => {
    const { button } = mountSwitcher()

    const code = button.querySelector('[translate="no"]')
    expect(code?.textContent).toBe('EN')
    const name = button.querySelector('[lang="de"]')
    expect(name?.textContent).toBe('Deutsch')
    expect(button.textContent).toContain('Switch language to Deutsch')
  })

  it('switches, stores the choice, announces in German, and names English', async () => {
    const { i18n, button, status } = mountSwitcher()
    expect(status()).toBe('')

    button.click()
    await nextTick()

    expect(i18n.global.locale.value).toBe('de')
    expect(localStorage.getItem('flowseer.locale')).toBe('de')
    expect(status()).toBe('Sprache auf Deutsch umgestellt.')
    expect(
      document.body.querySelector('[role="status"] [lang="de"]')?.textContent,
    ).toBe('Deutsch')
    expect(button.querySelector('[translate="no"]')?.textContent).toBe('DE')
    expect(button.querySelector('[lang="en"]')?.textContent).toBe('English')
    expect(button.textContent).toContain('Sprache auf English umstellen')
  })

  it('announces in English after switching back', async () => {
    const { i18n, button, status } = mountSwitcher('de')

    button.click()
    await nextTick()

    expect(i18n.global.locale.value).toBe('en')
    expect(localStorage.getItem('flowseer.locale')).toBe('en')
    expect(status()).toBe('Language set to English.')
  })

  it('still switches and announces the unsaved notice when storage throws on write', async () => {
    vi.stubGlobal('localStorage', {
      clear: () => {},
      getItem: () => null,
      setItem: () => {
        throw new Error('blocked')
      },
    })
    const { i18n, button, status } = mountSwitcher()

    button.click()
    await nextTick()

    expect(i18n.global.locale.value).toBe('de')
    expect(status()).toBe(
      'Sprache für diese Seite geändert. Der Browser konnte die Einstellung nicht speichern.',
    )
  })

  it('follows an outside locale change in the name and the status', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { i18n, button, status } = mountSwitcher()

    button.click()
    await nextTick()
    i18n.global.locale.value = 'en'
    await nextTick()

    expect(status()).toBe('Language set to English.')
    expect(button.textContent).toContain('Switch language to Deutsch')
    expect(i18nWarnings(warn.mock.calls)).toEqual([])
  })
})
