// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h } from 'vue'
import UiMeter from './UiMeter.vue'
import UiSegmentedMeter from './UiSegmentedMeter.vue'
import { createWebI18n, type WebLocale } from '../../i18n'

let dispose = () => {}
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})

function mount(
  component: Component,
  props: Record<string, unknown> = {},
  locale: WebLocale = 'en',
) {
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render() {
      return h(component, props)
    },
  })
  const i18n = createWebI18n(locale)
  app.use(i18n)
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  const el = host.firstElementChild as HTMLElement
  return { host, el, i18n }
}

describe('UiMeter', () => {
  it('single meter sets role="meter", ARIA value bounds, and computes warning/critical tones based on percentage', () => {
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

    const warning = mount(UiMeter, { label: 'Memory', value: 80 })
    const warningBar = warning.el.querySelector('[role="meter"] i')
    expect(warningBar?.className).toContain('bg-warning-foreground')
    dispose()

    const critical = mount(UiMeter, { label: 'Bandwidth', value: 95 })
    const criticalBar = critical.el.querySelector('[role="meter"] i')
    expect(criticalBar?.className).toContain('bg-danger-foreground')
    dispose()
  })

  it('formats 1234.5 as 1.234,5 in de with a non-breaking space before default unit', () => {
    const { el } = mount(
      UiMeter,
      { label: 'Speicherauslastung', value: 1234.5 },
      'de',
    )
    expect(el.textContent).toContain('1.234,5\u00a0%')
  })

  it('supports custom unit, detailSeparator, and valueText formatter overrides', () => {
    const withCustomUnit = mount(UiMeter, {
      label: 'Memory',
      value: 50,
      unit: 'MB',
      detail: '2 of 4 slots',
      detailSeparator: ' -- ',
    })
    expect(withCustomUnit.el.textContent).toContain('50\u00a0MB')
    expect(withCustomUnit.el.textContent).toContain(' -- 2 of 4 slots')
    dispose()

    const withValueText = mount(UiMeter, {
      label: 'Disk',
      value: 75,
      valueText: (value: number, unit?: string) => `Used: ${value} ${unit}`,
    })
    expect(withValueText.el.textContent).toContain('Used: 75 %')
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

  it('joins German summary phrases with und and translates status labels', () => {
    const { el } = mount(
      UiSegmentedMeter,
      {
        counts: { Healthy: 10, Degraded: 2, Offline: 1 },
        legend: true,
      },
      'de',
    )

    const track = el.querySelector('[role="img"]')
    expect(track?.getAttribute('aria-label')).toBe(
      '10 Gesund, 2 Beeinträchtigt und 1 Offline',
    )

    const legendItems = el.querySelectorAll('li')
    expect(legendItems[0]?.textContent).toContain('Gesund')
    expect(legendItems[1]?.textContent).toContain('Beeinträchtigt')
    expect(legendItems[2]?.textContent).toContain('Offline')
  })

  it('renders zero count as n(0) in summary', () => {
    const { el } = mount(UiSegmentedMeter, {
      counts: { Healthy: 0, Degraded: 0, Offline: 0 },
    })
    const track = el.querySelector('[role="img"]')
    expect(track?.getAttribute('aria-label')).toBe('0')
  })

  it('supports labels override map and segmentText formatter override', () => {
    const { el } = mount(UiSegmentedMeter, {
      counts: { Healthy: 5, Degraded: 1 },
      labels: { Healthy: 'Operational' },
      segmentText: (count: number, label: string) => `${label}: [${count}]`,
    })

    const track = el.querySelector('[role="img"]')
    expect(track?.getAttribute('aria-label')).toBe(
      'Operational: [5], Degraded: [1]',
    )
  })

  it('renders explicit segments correctly', () => {
    const { el } = mount(UiSegmentedMeter, {
      segments: [
        { label: 'Active', count: 4, tone: 'success' },
        { label: 'Standby', count: 1, tone: 'info' },
      ],
      legend: true,
    })

    const track = el.querySelector('[role="img"]')
    expect(track?.getAttribute('aria-label')).toBe('4 Active, 1 Standby')

    const legendItems = el.querySelectorAll('li')
    expect(legendItems.length).toBe(2)
    expect(legendItems[0]?.textContent).toContain('Active')
    expect(legendItems[1]?.textContent).toContain('Standby')
  })
})
