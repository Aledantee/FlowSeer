// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Component } from 'vue'
import { createApp, h, nextTick } from 'vue'
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

function mountLive(children: () => ReturnType<typeof h>[]) {
  const host = document.createElement('div')
  document.body.append(host)
  const i18n = createWebI18n('en')
  const app = createApp({
    render() {
      return h('div', children())
    },
  })
  app.use(i18n)
  app.mount(host)
  dispose = () => {
    app.unmount()
    dispose = () => {}
  }
  return { host, i18n }
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

  it('renders a small fraction unrounded in en and de', () => {
    const en = mount(UiMeter, { label: 'Loss', value: 0.0004 })
    expect(en.el.textContent).toContain('0.0004\u00a0%')
    dispose()

    const de = mount(UiMeter, { label: 'Verlust', value: 0.0004 }, 'de')
    expect(de.el.textContent).toContain('0,0004\u00a0%')
  })

  it('renders exactly the formatted value for an explicit empty unit', () => {
    const { el } = mount(UiMeter, { label: 'Queue', value: 1234.5, unit: '' })
    const valueEl = el.querySelector('.font-mono')
    expect(valueEl?.textContent?.trim()).toBe('1,234.5')
    expect(valueEl?.textContent).not.toContain('\u00a0')
  })

  it('takes the default unit from the catalog when unit is unset', () => {
    const { el } = mount(UiMeter, { label: 'CPU', value: 42 })
    expect(el.querySelector('.font-mono')?.textContent?.trim()).toBe(
      '42\u00a0%',
    )
  })

  it('follows a live locale switch for the number format and keeps explicit overrides', async () => {
    const { host, i18n } = mountLive(() => [
      h(UiMeter, { label: 'Default', value: 1234.5 }),
      h(UiMeter, { label: 'Unit', value: 1234.5, unit: 'MB' }),
      h(UiMeter, {
        label: 'Custom',
        value: 1234.5,
        valueText: (value: number, unit?: string) => `Used: ${value} ${unit}`,
      }),
    ])
    const values = () =>
      Array.from(host.querySelectorAll('.font-mono')).map((el) =>
        el.textContent?.trim(),
      )
    expect(values()).toEqual([
      '1,234.5\u00a0%',
      '1,234.5\u00a0MB',
      'Used: 1234.5 %',
    ])

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(values()).toEqual([
      '1.234,5\u00a0%',
      '1.234,5\u00a0MB',
      'Used: 1234.5 %',
    ])
  })
})

describe('UiSegmentedMeter', () => {
  it('keeps an empty caller label instead of the catalog label', () => {
    const { el } = mount(UiSegmentedMeter, {
      counts: { Healthy: 3, Degraded: 1 },
      labels: { Healthy: '' },
      legend: true,
    })
    const items = el.querySelectorAll('li')
    expect(items[0]?.querySelector('span')?.textContent).toBe('')
    expect(items[1]?.querySelector('span')?.textContent).toBe('Degraded')
  })

  it('keeps a data-derived segment label that names an Object.prototype member', () => {
    const { el } = mount(UiSegmentedMeter, {
      segments: [
        { label: 'constructor', count: 2, tone: 'success' },
        { label: 'toString', count: 1, tone: 'info' },
      ],
      labels: { Other: 'Unrelated' },
      legend: true,
    })
    const track = el.querySelector('[role="img"]')
    expect(track?.getAttribute('aria-label')).toBe('2 constructor, 1 toString')
    const items = Array.from(el.querySelectorAll('li')).map(
      (li) => li.querySelector('span')?.textContent,
    )
    expect(items).toEqual(['constructor', 'toString'])
  })

  it('does not resolve a prototype member as a counts key label', () => {
    const { el } = mount(UiSegmentedMeter, {
      counts: { Healthy: 1 },
      labels: { Healthy: 'Up' },
      legend: true,
    })
    const items = Array.from(el.querySelectorAll('li')).map(
      (li) => li.querySelector('span')?.textContent,
    )
    expect(items).toEqual(['Up', 'Degraded', 'Offline'])
  })

  it('applies the labels map to explicit segments', () => {
    const { el } = mount(UiSegmentedMeter, {
      segments: [
        { label: 'Active', count: 4, tone: 'success' },
        { label: 'Standby', count: 1, tone: 'info' },
      ],
      labels: { Active: 'In service' },
      legend: true,
    })
    expect(el.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe(
      '4 In service, 1 Standby',
    )
    expect(el.querySelector('li span')?.textContent).toBe('In service')
  })

  it('titles each segment with the formatted count and translated label in en and de', () => {
    const cases: { locale: WebLocale; titles: string[] }[] = [
      { locale: 'en', titles: ['1,234 Healthy', '2 Degraded'] },
      { locale: 'de', titles: ['1.234 Gesund', '2 Beeinträchtigt'] },
    ]
    for (const { locale, titles } of cases) {
      const { el } = mount(
        UiSegmentedMeter,
        { counts: { Healthy: 1234, Degraded: 2 } },
        locale,
      )
      const segments = Array.from(el.querySelectorAll('.health-segment'))
      expect(segments.map((s) => s.getAttribute('title'))).toEqual(titles)
      dispose()
    }
  })

  it('formats the legend count in en and de', () => {
    const cases: { locale: WebLocale; counts: string[] }[] = [
      { locale: 'en', counts: ['1,234', '2', '0'] },
      { locale: 'de', counts: ['1.234', '2', '0'] },
    ]
    for (const { locale, counts } of cases) {
      const { el } = mount(
        UiSegmentedMeter,
        { counts: { Healthy: 1234, Degraded: 2 }, legend: true },
        locale,
      )
      const shown = Array.from(el.querySelectorAll('li strong')).map(
        (strong) => strong.textContent,
      )
      expect(shown).toEqual(counts)
      dispose()
    }
  })

  it('follows a live locale switch for labels, list joining, and counts', async () => {
    const { host, i18n } = mountLive(() => [
      h(UiSegmentedMeter, {
        counts: { Healthy: 1234, Degraded: 2, Offline: 1 },
        legend: true,
      }),
      h(UiSegmentedMeter, {
        segments: [{ label: 'Active', count: 4, tone: 'success' }],
        labels: { Active: 'In service' },
        segmentText: (count: number, label: string) => `${label}: [${count}]`,
        legend: true,
      }),
    ])
    const read = (index: number) => {
      const meter = host.children[0]!.children[index]!
      return {
        summary: meter
          .querySelector('[role="img"]')
          ?.getAttribute('aria-label'),
        titles: Array.from(meter.querySelectorAll('.health-segment')).map((s) =>
          s.getAttribute('title'),
        ),
        legend: Array.from(meter.querySelectorAll('li')).map(
          (li) =>
            `${li.querySelector('span')?.textContent} ${li.querySelector('strong')?.textContent}`,
        ),
      }
    }

    expect(read(0)).toEqual({
      summary: '1,234 Healthy, 2 Degraded, 1 Offline',
      titles: ['1,234 Healthy', '2 Degraded', '1 Offline'],
      legend: ['Healthy 1,234', 'Degraded 2', 'Offline 1'],
    })
    const override = read(1)
    expect(override).toEqual({
      summary: 'In service: [4]',
      titles: ['In service: [4]'],
      legend: ['In service 4'],
    })

    i18n.global.locale.value = 'de'
    await nextTick()

    expect(read(0)).toEqual({
      summary: '1.234 Gesund, 2 Beeinträchtigt und 1 Offline',
      titles: ['1.234 Gesund', '2 Beeinträchtigt', '1 Offline'],
      legend: ['Gesund 1.234', 'Beeinträchtigt 2', 'Offline 1'],
    })
    expect(read(1)).toEqual(override)
  })
})
