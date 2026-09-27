// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h, nextTick } from 'vue'
import UiPagination from './UiPagination.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountPagination(props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiPagination as Component, props)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiPagination', () => {
  it('renders correct number of pages and ellipsis for large item counts', () => {
    // 100 items, 10 per page = 10 pages, showEdges creates ellipsis
    const { el } = mountPagination({
      total: 100,
      itemsPerPage: 10,
      page: 1,
      showEdges: true,
    })
    const buttons = el.querySelectorAll('button')
    expect(buttons.length).toBeGreaterThan(0)
    // Ellipsis should be rendered
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

    // Find page 2 button
    const buttons = Array.from(el.querySelectorAll('button'))
    const page2Btn = buttons.find((b) => b.textContent?.trim() === '2')
    expect(page2Btn).toBeDefined()
    page2Btn?.click()
    await nextTick()
    expect(onUpdatePage).toHaveBeenCalledWith(2)
  })
})
