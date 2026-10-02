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

function buttonByLabel(root: Element, label: string): HTMLButtonElement {
  const button = Array.from(root.querySelectorAll('button')).find(
    (b) => b.getAttribute('aria-label') === label,
  )
  if (!button) throw new Error(`Missing button labelled ${label}`)
  return button
}

function markOf(button: Element): string | undefined {
  return button.querySelector('[aria-hidden="true"]')?.textContent?.trim()
}

// The text a sighted user reads, which an accessible name must contain.
function visibleTextOf(button: Element): string {
  return Array.from(button.childNodes)
    .filter(
      (node) =>
        !(
          node instanceof Element && node.getAttribute('aria-hidden') === 'true'
        ),
    )
    .map((node) => node.textContent ?? '')
    .join('')
    .trim()
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

  it('supports every glyph and text override, each inside its own button', () => {
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
      firstMark: '⏮',
      previousMark: '◀',
      nextMark: '▶',
      lastMark: '⏭',
      ellipsis: '---',
    })

    const first = buttonByLabel(el, 'Jump to beginning')
    const previous = buttonByLabel(el, 'Step back')
    const next = buttonByLabel(el, 'Step forward')
    const last = buttonByLabel(el, 'Jump to end')

    expect(first.textContent?.trim()).toBe('⏮')
    expect(previous.textContent?.trim()).toBe('◀Back')
    expect(next.textContent?.trim()).toBe('Forward▶')
    expect(last.textContent?.trim()).toBe('⏭')
    expect(markOf(previous)).toBe('◀')
    expect(markOf(next)).toBe('▶')
    expect(el.textContent).toContain('---')
  })

  it('takes the first, previous, next, and last labels from the de catalog', () => {
    const { el } = mountPagination(
      { total: 100, itemsPerPage: 10, page: 5, showEdges: true },
      'de',
    )

    const labels = Array.from(el.querySelectorAll('button[aria-label]')).map(
      (b) => b.getAttribute('aria-label'),
    )
    expect(labels[0]).toBe('Erste Seite')
    expect(labels[1]).toBe('Zurück zur vorherigen Seite')
    expect(labels[labels.length - 2]).toBe('Weiter zur nächsten Seite')
    expect(labels[labels.length - 1]).toBe('Letzte Seite')
  })

  it('formats a page above 999 as text and as page label in en and de', () => {
    const cases: { locale: WebLocale; text: string; label: string }[] = [
      { locale: 'en', text: '1,234', label: 'Page 1,234' },
      { locale: 'de', text: '1.234', label: 'Seite 1.234' },
    ]
    for (const { locale, text, label } of cases) {
      const { el } = mountPagination(
        { total: 20000, itemsPerPage: 10, page: 1234 },
        locale,
      )
      const button = buttonByLabel(el, label)
      expect(button.textContent?.trim()).toBe(text)
      dispose()
    }
  })

  it('includes the visible text of the previous and next buttons in their accessible name', () => {
    for (const locale of ['en', 'de'] as const) {
      const { el } = mountPagination(
        { total: 100, itemsPerPage: 10, page: 5 },
        locale,
      )
      const buttons = el.querySelectorAll('button')
      const previous = buttons[0]!
      const next = buttons[buttons.length - 1]!
      for (const button of [previous, next]) {
        const visible = visibleTextOf(button)
        expect(visible).not.toBe('')
        expect(button.getAttribute('aria-label')?.toLowerCase()).toContain(
          visible.toLowerCase(),
        )
      }
      dispose()
    }
  })

  it('follows a live locale switch for labels, texts, and page labels and keeps explicit overrides', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const i18n = createWebI18n('en')
    const app = createApp({
      render() {
        return h('div', [
          h(UiPagination as Component, {
            total: 20000,
            itemsPerPage: 10,
            page: 1234,
            showEdges: true,
          }),
          h(UiPagination as Component, {
            total: 20000,
            itemsPerPage: 10,
            page: 1234,
            showEdges: true,
            firstLabel: 'Jump to beginning',
            previousLabel: 'Step back',
            nextLabel: 'Step forward',
            lastLabel: 'Jump to end',
            previousText: 'Back',
            nextText: 'Forward',
            pageLabel: (page: number) => `Custom ${page}`,
          }),
        ])
      },
    })
    app.use(i18n)
    app.mount(host)
    dispose = () => {
      app.unmount()
      dispose = () => {}
    }

    const read = (index: number) => {
      const pagination = host.firstElementChild!.children[index]!
      return Array.from(pagination.querySelectorAll('button')).map(
        (b) => `${b.getAttribute('aria-label')}|${visibleTextOf(b)}`,
      )
    }
    const defaults = (labels: string[]) => [
      labels[0],
      labels[1],
      labels[labels.length - 2],
      labels[labels.length - 1],
    ]

    const enDefault = read(0)
    expect(defaults(enDefault)).toEqual([
      'First page|',
      'Previous page|Previous',
      'Next page|Next',
      'Last page|',
    ])
    expect(enDefault).toContain('Page 1,234|1,234')
    const overrideLabels = read(1).map((entry) => entry.split('|')[0])
    expect(overrideLabels).toContain('Custom 1234')

    i18n.global.locale.value = 'de'
    await nextTick()

    const deDefault = read(0)
    expect(defaults(deDefault)).toEqual([
      'Erste Seite|',
      'Zurück zur vorherigen Seite|Zurück',
      'Weiter zur nächsten Seite|Weiter',
      'Letzte Seite|',
    ])
    expect(deDefault).toContain('Seite 1.234|1.234')
    expect(read(1).map((entry) => entry.split('|')[0])).toEqual(overrideLabels)
    expect(read(1)).toContain('Step back|Back')
    expect(read(1)).toContain('Step forward|Forward')
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
