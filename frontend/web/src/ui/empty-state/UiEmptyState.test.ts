// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h } from 'vue'
import UiEmptyState from './UiEmptyState.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mountEmptyState(
  props: Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(UiEmptyState as Component, props, slots)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiEmptyState', () => {
  it('renders title, description, and custom action slot', () => {
    const { el } = mountEmptyState(
      {
        title: 'No clients found',
        description: 'Try adjusting your search filters or site scope.',
      },
      {
        icon: () => h('span', { class: 'custom-icon' }, '🔍'),
        actions: () =>
          h('button', { type: 'button', class: 'custom-btn' }, 'Reset filter'),
      },
    )

    expect(el.textContent).toContain('No clients found')
    expect(el.textContent).toContain('Try adjusting your search filters')
    expect(el.querySelector('.custom-icon')).not.toBeNull()
    expect(el.querySelector('.custom-btn')).not.toBeNull()
    expect(el.querySelector('.custom-btn')?.textContent).toBe('Reset filter')
  })
})
