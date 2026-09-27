// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h } from 'vue'
import UiMeter from './UiMeter.vue'
import UiSegmentedMeter from './UiSegmentedMeter.vue'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mount(component: Component, props: Record<string, unknown> = {}) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(component, props)
    },
  })
  app.mount(host)
  dispose = () => app.unmount()
  const el = host.firstElementChild as HTMLElement
  return { host, el }
}

describe('UiMeter', () => {
  it('single meter sets role="meter", ARIA value bounds, and computes warning/critical tones based on percentage', () => {
    // Normal tone (< 75%)
    const normal = mount(UiMeter, { label: 'CPU Usage', value: 45 })
    const normalMeter = normal.el.querySelector('[role="meter"]')
    expect(normalMeter).not.toBeNull()
    expect(normalMeter?.getAttribute('aria-valuenow')).toBe('45')
    expect(normalMeter?.getAttribute('aria-valuemin')).toBe('0')
    expect(normalMeter?.getAttribute('aria-valuemax')).toBe('100')
    expect(normalMeter?.getAttribute('aria-label')).toBe('CPU Usage')
    const normalBar = normalMeter?.querySelector('i')
    expect(normalBar?.className).toContain('bg-chart-1')
    dispose()

    // Warning tone (>= 75% and < 90%)
    const warning = mount(UiMeter, { label: 'Memory', value: 80 })
    const warningBar = warning.el.querySelector('[role="meter"] i')
    expect(warningBar?.className).toContain('bg-warning-foreground')
    dispose()

    // Critical tone (>= 90%)
    const critical = mount(UiMeter, { label: 'Bandwidth', value: 95 })
    const criticalBar = critical.el.querySelector('[role="meter"] i')
    expect(criticalBar?.className).toContain('bg-danger-foreground')
    dispose()
  })

  it('segmented meter computes summary aria-label and renders proportional segment flex-grow values and legend list', () => {
    const { el } = mount(UiSegmentedMeter, {
      counts: { Healthy: 10, Degraded: 2, Offline: 1 },
      legend: true,
    })

    const track = el.querySelector('[role="img"]')
    expect(track).not.toBeNull()
    expect(track?.getAttribute('aria-label')).toBe(
      '10 Healthy, 2 Degraded, 1 Offline',
    )

    const segments = track?.querySelectorAll('.health-segment')
    expect(segments?.length).toBe(3)
    expect((segments?.[0] as HTMLElement).style.flexGrow).toBe('10')
    expect((segments?.[1] as HTMLElement).style.flexGrow).toBe('2')
    expect((segments?.[2] as HTMLElement).style.flexGrow).toBe('1')

    const legendItems = el.querySelectorAll('li')
    expect(legendItems.length).toBe(3)
    expect(legendItems[0]?.textContent).toContain('Healthy')
    expect(legendItems[0]?.textContent).toContain('10')
    expect(legendItems[1]?.textContent).toContain('Degraded')
    expect(legendItems[1]?.textContent).toContain('2')
    expect(legendItems[2]?.textContent).toContain('Offline')
    expect(legendItems[2]?.textContent).toContain('1')
  })
})
