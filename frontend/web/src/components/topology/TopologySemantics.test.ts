// @vitest-environment happy-dom
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { computed, createApp, defineComponent, h, provide, ref } from 'vue'
import type { Component } from 'vue'
import { Position } from '@vue-flow/core'
import { devices, linksOf } from '../../domain/fleet'
import type { Device, Link } from '../../domain/fleet'
import { topologyLive } from './live'
import type { Selection } from './live'
import TopologyLink from './TopologyLink.vue'
import TopologyNode from './TopologyNode.vue'

vi.mock('@vue-flow/core', async () => {
  const vue = await import('vue')
  return {
    BaseEdge: vue.defineComponent({
      inheritAttrs: false,
      setup(_, { attrs }) {
        return () => vue.h('path', { ...attrs, class: 'vue-flow__edge-path' })
      },
    }),
    EdgeLabelRenderer: vue.defineComponent({
      setup(_, { slots }) {
        return () => slots.default?.()
      },
    }),
    Handle: vue.defineComponent({
      setup(_, { attrs }) {
        return () => vue.h('span', attrs)
      },
    }),
    Position: { Top: 'top', Right: 'right', Bottom: 'bottom', Left: 'left' },
    getSmoothStepPath: () => ['M 0 0 L 10 10'],
  }
})

let dispose = () => {}

afterEach(() => {
  dispose()
  dispose = () => {}
  document.body.replaceChildren()
})

function mount(component: Component, props: Record<string, unknown>) {
  const host = document.createElement('div')
  document.body.append(host)
  const fleet = computed(() => devices)
  const links = linksOf(devices)
  const selection = ref<Selection>()
  const hovered = ref<string>()
  const select = vi.fn((next: Selection | undefined) => {
    selection.value = next
  })
  const app = createApp(
    defineComponent({
      setup() {
        provide(topologyLive, {
          fleet,
          highlighted: computed(() => undefined),
          device: (id: string): Device | undefined =>
            fleet.value.find((device) => device.id === id),
          link: (id: string): Link | undefined =>
            links.find((link) => link.id === id),
          selection,
          hovered,
          select,
        })
        return () => h(component, props)
      },
    }),
  )
  app.mount(host)
  dispose = () => app.unmount()
  return host
}

describe('topology assumption semantics', () => {
  it('renders status badges only for failing nodes', () => {
    const healthy = devices.find((device) => device.health === 'Healthy')
    const degraded = devices.find((device) => device.health === 'Degraded')
    if (!healthy || !degraded) throw new Error('Missing health fixtures')

    let host = mount(TopologyNode, { data: { deviceId: healthy.id } })
    expect(host.querySelector('.topology-status')).toBeNull()
    expect(host.textContent).not.toContain('Healthy')
    dispose()

    host = mount(TopologyNode, { data: { deviceId: degraded.id } })
    expect(host.textContent).toContain('Degraded')
    expect(host.querySelector('.bg-warning-surface')).not.toBeNull()
  })

  it('states that rendered links are assumed', () => {
    const link = linksOf(devices)[0]
    if (!link) throw new Error('Missing link fixture')

    const host = mount(TopologyLink, {
      id: link.id,
      sourceX: 0,
      sourceY: 0,
      targetX: 10,
      targetY: 10,
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
      data: { linkId: link.id },
    })

    expect(
      host.querySelector('.topology-link-label')?.getAttribute('aria-label'),
    ).toContain('Assumed link, not yet discovered')
  })

  it('keeps the visible legend and base-link dash in component source', () => {
    const graph = readFileSync(
      path.resolve(__dirname, 'TopologyGraph.vue'),
      'utf8',
    )
    const link = readFileSync(
      path.resolve(__dirname, 'TopologyLink.vue'),
      'utf8',
    )
    const baseRule = link.match(
      /\.topology-link :deep\(\.vue-flow__edge-path\) \{([^}]*)\}/,
    )?.[1]

    expect(graph).toMatch(
      /class="topology-assumption"[^>]*>[\s\S]*Assumed link, not yet discovered/,
    )
    expect(baseRule).toContain('stroke-dasharray: 4 5')
  })
})
