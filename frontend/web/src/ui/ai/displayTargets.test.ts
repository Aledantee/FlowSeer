// @vitest-environment happy-dom
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, reactive, type Component } from 'vue'
import { createAiRegistry } from '../../ai'
import type { AiRegistry, AiTarget } from '../../ai'
import { createWebI18n } from '../../i18n'
import TrafficChart from '../../components/TrafficChart.vue'
import TrafficSparkline from '../../components/TrafficSparkline.vue'
import UiBadge from '../badge/UiBadge.vue'
import UiStatusBadge from '../badge/UiStatusBadge.vue'
import UiBreadcrumbPage from '../breadcrumb/UiBreadcrumbPage.vue'
import UiCard from '../card/UiCard.vue'
import UiMetricCard from '../card/UiMetricCard.vue'
import UiEmptyState from '../empty-state/UiEmptyState.vue'
import UiKbd from '../kbd/UiKbd.vue'
import UiMeter from '../meter/UiMeter.vue'
import UiSegmentedMeter from '../meter/UiSegmentedMeter.vue'
import UiProgress from '../progress/UiProgress.vue'
import UiTable from '../table/UiTable.vue'
import UiTableCell from '../table/UiTableCell.vue'
import UiTableEmpty from '../table/UiTableEmpty.vue'
import UiTableHead from '../table/UiTableHead.vue'
import UiTableRow from '../table/UiTableRow.vue'
import { aiRegistryKey } from './context'
import type { AiOriginRequest } from './context'

// happy-dom has no layout engine, so the chart's resize observer is inert.
beforeAll(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
})

let disposers: (() => void)[] = []
afterEach(() => {
  for (const dispose of disposers) dispose()
  disposers = []
  document.body.replaceChildren()
})

function target(id: string): AiTarget {
  return { id, kind: 'device', label: id, context: { entity: id } }
}

function origin(requestId: string): AiOriginRequest {
  return { requestId, action: 'summary', targets: [], history: [] }
}

// Where a component's native root has to sit to be valid markup.
type Parent = 'none' | 'table' | 'tbody' | 'tr'

interface Case {
  name: string
  component: Component
  props: Record<string, unknown>
  // The tag the component renders and registers.
  tag: string
  parent: Parent
}

const cases: Case[] = [
  {
    name: 'UiBadge',
    component: UiBadge,
    props: {},
    tag: 'SPAN',
    parent: 'none',
  },
  {
    name: 'UiStatusBadge',
    component: UiStatusBadge,
    props: { status: 'Healthy' },
    tag: 'SPAN',
    parent: 'none',
  },
  { name: 'UiCard', component: UiCard, props: {}, tag: 'DIV', parent: 'none' },
  {
    name: 'UiCard as article',
    component: UiCard,
    props: { as: 'article' },
    tag: 'ARTICLE',
    parent: 'none',
  },
  {
    name: 'UiMetricCard',
    component: UiMetricCard,
    props: { label: 'Clients', value: 12 },
    tag: 'ARTICLE',
    parent: 'none',
  },
  {
    name: 'UiEmptyState',
    component: UiEmptyState,
    props: { title: 'Nothing here' },
    tag: 'DIV',
    parent: 'none',
  },
  { name: 'UiKbd', component: UiKbd, props: {}, tag: 'KBD', parent: 'none' },
  {
    name: 'UiMeter',
    component: UiMeter,
    props: { label: 'CPU', value: 40 },
    tag: 'DIV',
    parent: 'none',
  },
  {
    name: 'UiSegmentedMeter',
    component: UiSegmentedMeter,
    props: { counts: { Healthy: 3, Offline: 1 } },
    tag: 'DIV',
    parent: 'none',
  },
  {
    name: 'UiProgress',
    component: UiProgress,
    props: { modelValue: 30 },
    tag: 'DIV',
    parent: 'none',
  },
  {
    name: 'UiTable',
    component: UiTable,
    props: {},
    tag: 'TABLE',
    parent: 'none',
  },
  {
    name: 'UiTableRow',
    component: UiTableRow,
    props: {},
    tag: 'TR',
    parent: 'tbody',
  },
  {
    name: 'UiTableCell',
    component: UiTableCell,
    props: {},
    tag: 'TD',
    parent: 'tr',
  },
  {
    name: 'UiTableHead',
    component: UiTableHead,
    props: {},
    tag: 'TH',
    parent: 'tr',
  },
  {
    name: 'UiTableHead sortable',
    component: UiTableHead,
    props: { sortable: true },
    tag: 'TH',
    parent: 'tr',
  },
  {
    name: 'UiTableEmpty',
    component: UiTableEmpty,
    props: { colSpan: 2 },
    tag: 'TR',
    parent: 'tbody',
  },
  {
    name: 'UiBreadcrumbPage',
    component: UiBreadcrumbPage,
    props: {},
    tag: 'SPAN',
    parent: 'none',
  },
  {
    name: 'UiBreadcrumbPage as',
    component: UiBreadcrumbPage,
    props: { as: 'h1' },
    tag: 'H1',
    parent: 'none',
  },
  {
    name: 'TrafficChart',
    component: TrafficChart,
    props: { label: 'Throughput', points: [{ hour: 0, mbps: 5 }] },
    tag: 'FIGURE',
    parent: 'none',
  },
  {
    name: 'TrafficSparkline',
    component: TrafficSparkline,
    props: { label: 'Series', values: [1, 2, 3] },
    tag: 'svg',
    parent: 'none',
  },
]

interface State {
  ai?: AiTarget
  aiOrigin?: AiOriginRequest
}

interface Mounted {
  host: HTMLElement
  registry: AiRegistry
  acknowledged: string[]
  state: State
  // The element the component rendered, found by its tag inside any wrapper.
  element: () => Element
  unmount: () => void
}

function wrap(parent: Parent, child: () => ReturnType<typeof h>) {
  switch (parent) {
    case 'none':
      return child()
    case 'tr':
      return h('table', [h('tbody', [h('tr', [child()])])])
    case 'tbody':
      return h('table', [h('tbody', [child()])])
    case 'table':
      return h('table', [child()])
  }
}

function mount(item: Case, initial: State = {}): Mounted {
  const registry = createAiRegistry()
  const state = reactive<State>({ ...initial })
  const acknowledged: string[] = []
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () =>
      wrap(item.parent, () =>
        h(item.component, {
          ...item.props,
          ai: state.ai,
          aiOrigin: state.aiOrigin,
          onAiOriginAcknowledged: (requestId: string) =>
            acknowledged.push(requestId),
        }),
      ),
  })
  app.provide(aiRegistryKey, registry)
  app.use(createWebI18n('en'))
  app.mount(host)
  let mounted = true
  const unmount = () => {
    if (!mounted) return
    mounted = false
    app.unmount()
  }
  disposers.push(unmount)
  const element = () => {
    const found = [...host.querySelectorAll('*')].find(
      (node) => node.tagName === item.tag,
    )
    if (!found) throw new Error(`Missing ${item.tag} in ${item.name}`)
    return found
  }
  return { host, registry, acknowledged, state, element, unmount }
}

const marker = (node: Element) => node.getAttribute('data-ai-origin')

describe.each(cases)('$name', (item) => {
  it('registers nothing and marks nothing without its props', async () => {
    const { registry, element } = mount(item)
    await nextTick()

    expect(registry.list()).toEqual([])
    expect(marker(element())).toBeNull()
  })

  it('registers its own tag, follows the target, and drops it on removal', async () => {
    const { registry, state, element, unmount } = mount(item, {
      ai: target('d1'),
    })
    await nextTick()

    const rendered = element()
    expect(rendered.tagName).toBe(item.tag)
    expect(registry.list().map((entry) => entry.id)).toEqual(['d1'])
    expect(registry.view('d1')?.element).toBe(rendered)

    state.ai = target('d2')
    await nextTick()
    expect(registry.list().map((entry) => entry.id)).toEqual(['d2'])
    expect(registry.view('d2')?.element).toBe(rendered)

    state.ai = undefined
    await nextTick()
    expect(registry.list()).toEqual([])

    state.ai = target('d3')
    await nextTick()
    expect(registry.list().map((entry) => entry.id)).toEqual(['d3'])

    unmount()
    document.body.append(rendered)
    expect(registry.list()).toEqual([])
  })

  it('marks an agent change, keeps it through selection, and acknowledges it once', async () => {
    const { registry, acknowledged, state, element } = mount(item, {
      ai: target('d1'),
      aiOrigin: origin('r1'),
    })
    await nextTick()
    const rendered = element()
    expect(marker(rendered)).toBe('agent')

    expect(registry.highlight('d1')).toBe(true)
    expect(rendered.hasAttribute('data-ai-selected')).toBe(true)
    expect(marker(rendered)).toBe('agent')

    rendered.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    rendered.dispatchEvent(new Event('keydown', { bubbles: true }))
    await nextTick()
    expect(acknowledged).toEqual(['r1'])
    expect(marker(rendered)).toBeNull()
    expect(rendered.hasAttribute('data-ai-selected')).toBe(true)

    state.aiOrigin = { ...origin('r1') }
    await nextTick()
    expect(marker(rendered)).toBeNull()

    state.aiOrigin = origin('r2')
    await nextTick()
    expect(marker(rendered)).toBe('agent')

    state.aiOrigin = undefined
    await nextTick()
    expect(marker(rendered)).toBeNull()
    rendered.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    expect(acknowledged).toEqual(['r1'])
  })

  it('marks an agent change without registering a target', async () => {
    const { registry, element } = mount(item, { aiOrigin: origin('r1') })
    await nextTick()

    expect(marker(element())).toBe('agent')
    expect(registry.list()).toEqual([])
  })
})

describe('chart roots', () => {
  it('keeps the SVG sparkline root and label when it registers', async () => {
    const plain = mount(cases.find((c) => c.name === 'TrafficSparkline')!)
    await nextTick()
    const before = plain.host.innerHTML

    const { host, registry } = mount(
      cases.find((c) => c.name === 'TrafficSparkline')!,
      { ai: target('chart:s1') },
    )
    await nextTick()

    const svg = host.firstElementChild
    expect(host.children).toHaveLength(1)
    expect(svg).toBeInstanceOf(SVGElement)
    expect(svg?.getAttribute('role')).toBe('img')
    expect(svg?.getAttribute('aria-label')).toBe('Series')
    expect(registry.view('chart:s1')?.element).toBe(svg)
    expect(host.innerHTML).toBe(before)
  })

  it('keeps the chart figure root and its labelled frame', async () => {
    const { host, registry } = mount(
      cases.find((c) => c.name === 'TrafficChart')!,
      { ai: target('chart:c1') },
    )
    await nextTick()

    const figure = host.firstElementChild
    expect(figure?.tagName).toBe('FIGURE')
    expect(registry.view('chart:c1')?.element).toBe(figure)
    expect(
      figure?.querySelector('[role="img"]')?.getAttribute('aria-label'),
    ).toContain('Throughput')
  })
})
