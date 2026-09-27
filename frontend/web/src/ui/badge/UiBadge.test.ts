// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h } from 'vue'
import UiBadge from './UiBadge.vue'
import UiStatusBadge from './UiStatusBadge.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mount(
  component: Component,
  props: Record<string, unknown> = {},
  slots: Record<string, () => unknown> = {},
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(component, props, slots)
    },
  })
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiBadge', () => {
  it('renders all eight variant color classes correctly', () => {
    const variants = [
      { variant: 'default' as const, expected: 'bg-subtle' },
      { variant: 'outline' as const, expected: 'bg-transparent' },
      { variant: 'primary' as const, expected: 'bg-primary' },
      { variant: 'accent' as const, expected: 'text-accent-foreground' },
      {
        variant: 'success' as const,
        expected: 'bg-success-surface',
      },
      {
        variant: 'warning' as const,
        expected: 'bg-warning-surface',
      },
      { variant: 'danger' as const, expected: 'bg-danger-surface' },
      { variant: 'info' as const, expected: 'bg-info-surface' },
    ]
    for (const { variant, expected } of variants) {
      const { el } = mount(UiBadge, { variant }, { default: () => 'Test' })
      expect(el.className).toContain(expected)
      dispose()
    }
  })

  it('renders matching text and badge variant for Healthy, Degraded, and Offline', () => {
    const statuses = [
      {
        status: 'Healthy' as const,
        expectedClass: 'bg-success-surface',
        text: 'Healthy',
      },
      {
        status: 'Degraded' as const,
        expectedClass: 'bg-warning-surface',
        text: 'Degraded',
      },
      {
        status: 'Offline' as const,
        expectedClass: 'bg-danger-surface',
        text: 'Offline',
      },
    ]
    for (const { status, expectedClass, text } of statuses) {
      const { el } = mount(UiStatusBadge, { status })
      expect(el.textContent?.trim()).toBe(text)
      expect(el.className).toContain(expectedClass)
      expect(el.classList).toContain('!text-sm')
      dispose()
    }
  })
})
