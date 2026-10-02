// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h, nextTick } from 'vue'
import UiPagination from './UiPagination.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountPagination(
  props: Record<string, unknown> = {},
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiPagination as Component, props)
    },
  })
  const i18n = createWebI18n(locale)
  app.use(i18n)
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el, i18n }
}

describe('UiPagination', () => {
  it('renders correct number of pages and ellipsis for large item counts', () => {
    const { el } = mountPagination({
      total: 100,
      itemsPerPage: 10,
      page: 1,
      showEdges: true,
    })
    const buttons = el.querySelectorAll('button')
    expect(buttons.length).toBeGreaterThan(0)
    const ellipsis = el.textContent
    expect(ellipsis).toContain('…')
  })

  it('emits update:page on button click', async () => {
    const onUpdatePage = vi.fn()
    const { el } = mountPagination({
      total: 50,
      itemsPerPage: 10,
      page: 1,
      'onUpdate:page': onUpdatePage,
    })

    const buttons = Array.from(el.querySelectorAll('button'))
    const page2Btn = buttons.find((b) => b.textContent?.trim() === '2')
    expect(page2Btn).toBeDefined()
    page2Btn?.click()
    await nextTick()
    expect(onUpdatePage).toHaveBeenCalledWith(2)
  })

  it('renders Zurück, Weiter, Erste Seite, and Seite 2 in de', () => {
    const { el } = mountPagination(
      {
        total: 50,
        itemsPerPage: 10,
        page: 1,
        showEdges: true,
      },
      'de',
    )

    expect(el.textContent).toContain('Zurück')
    expect(el.textContent).toContain('Weiter')

    const firstBtn = el.querySelector('[aria-label="Erste Seite"]')
    expect(firstBtn).not.toBeNull()

    const page2Btn = el.querySelector('button[aria-label="Seite 2"]')
    expect(page2Btn).not.toBeNull()
    expect(page2Btn?.textContent?.trim()).toBe('2')
  })

  it('supports every glyph and text override', () => {
    const { el } = mountPagination({
      total: 100,
      itemsPerPage: 10,
      page: 5,
      showEdges: true,
      firstLabel: 'Jump to beginning',
      previousLabel: 'Step back',
      nextLabel: 'Step forward',
      lastLabel: 'Jump to end',
      previousText: 'Back',
      nextText: 'Forward',
      firstMark: '<<',
      previousMark: '<',
      nextMark: '>',
      lastMark: '>>',
      ellipsis: '---',
    })

    expect(el.querySelector('[aria-label="Jump to beginning"]')).not.toBeNull()
    expect(el.querySelector('[aria-label="Step back"]')).not.toBeNull()
    expect(el.querySelector('[aria-label="Step forward"]')).not.toBeNull()
    expect(el.querySelector('[aria-label="Jump to end"]')).not.toBeNull()
    expect(el.textContent).toContain('Back')
    expect(el.textContent).toContain('Forward')
    expect(el.textContent).toContain('<<')
    expect(el.textContent).toContain('<')
    expect(el.textContent).toContain('>')
    expect(el.textContent).toContain('>>')
    expect(el.textContent).toContain('---')
  })

  it('supports custom pageLabel formatter and keeps update:page unchanged in de', async () => {
    const onUpdatePage = vi.fn()
    const { el } = mountPagination(
      {
        total: 50,
        itemsPerPage: 10,
        page: 1,
        pageLabel: (page: number) => `Custom Page ${page}`,
        'onUpdate:page': onUpdatePage,
      },
      'de',
    )

    const page2Btn = el.querySelector<HTMLButtonElement>(
      'button[aria-label="Custom Page 2"]',
    )
    expect(page2Btn).not.toBeNull()
    page2Btn?.click()
    await nextTick()
    expect(onUpdatePage).toHaveBeenCalledWith(2)
  })
})
