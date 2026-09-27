<script setup lang="ts">
import { UiTooltip } from '../../ui'
import {
  computed,
  nextTick,
  onMounted,
  onUnmounted,
  provide,
  useId,
  ref,
  watch,
} from 'vue'
import { VueFlow, getRectOfNodes, useVueFlow } from '@vue-flow/core'
import type { Edge, EdgeChange, Node, NodeChange, Rect } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { ControlButton, Controls } from '@vue-flow/controls'
import AppIcon from '../AppIcon.vue'
import { useWorkspace } from '../../navigation/workspace'
import { usePage } from '../../navigation/page'
import { MiniMap } from '@vue-flow/minimap'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/controls/dist/style.css'
import '@vue-flow/minimap/dist/style.css'
import type { Device, Site } from '../../domain/fleet'
import { linksOf } from '../../domain/fleet'
import { layoutTopology } from './layout'
import { topologyLive } from './live'
import type { Selection } from './live'
import TopologyInspector from './TopologyInspector.vue'
import TopologyLink from './TopologyLink.vue'
import TopologyNode from './TopologyNode.vue'
import TopologySiteNode from './TopologySiteNode.vue'

const props = defineProps<{
  fleet: Device[]
  sites: Site[]
  siteName: (id: string) => string
  tenantName: (siteId: string) => string
  // "deviceId~port" of an interface to open selected, e.g. from search.
  focus?: string
}>()
const SAMPLES = 40
const members = computed(() =>
  props.fleet.filter((device) =>
    props.sites.some((site) => site.id === device.siteId),
  ),
)
const links = computed(() => linksOf(members.value))
const devicesById = computed(
  () => new Map(members.value.map((device) => [device.id, device])),
)
const linksById = computed(
  () => new Map(links.value.map((link) => [link.id, link])),
)
const selection = ref<Selection>()
const hovered = ref<string>()
function select(next: Selection | undefined) {
  selection.value = next
}
// The device open in the side pane is marked on the main pane's graph.
const workspace = useWorkspace()
const page = usePage()
const highlighted = computed(() =>
  page.primary ? workspace.sideDeviceId.value : undefined,
)
provide(topologyLive, {
  highlighted,
  fleet: members,
  device: (id) => devicesById.value.get(id),
  link: (id) => linksById.value.get(id),
  selection,
  hovered,
  select,
})

// Only a change in who connects to whom re-runs the layout; traffic updates
// flow through the live lookups above.
const structure = computed(() =>
  JSON.stringify([
    props.sites.map((site) => site.id),
    members.value.map((device) => [device.id, device.siteId, device.uplinkId]),
  ]),
)
const nodes = ref<Node[]>([])
const edges = ref<Edge[]>([])
const failed = ref(false)
const frame = ref<HTMLElement>()
// Each graph owns its store: a page beside another can show a second
// topology, and a shared store would mix their nodes.
const flowId = `topology-${useId()}`
const {
  fitBounds,
  findNode,
  onNodesInitialized,
  viewport,
  dimensions,
  getNodes,
} = useVueFlow(flowId)
const TRANSITION_MS = 420
const ease = (t: number) =>
  t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2
function motionAllowed() {
  return !window.matchMedia('(prefers-reduced-motion: reduce)').matches
}
function topbarHeight(): number {
  return frame.value
    ? parseFloat(
        getComputedStyle(frame.value).getPropertyValue('--topbar-height'),
      ) || 0
    : 0
}
// The canvas runs under the glass top bar, so the framed graph keeps clear
// of it.
let framed: Rect | undefined
function frameGraph(animate: boolean) {
  if (!framed || !dimensions.value.width) return false
  const inset = 48
  void fitBounds(framed, {
    padding: {
      top: `${topbarHeight() + inset}px`,
      right: `${inset}px`,
      bottom: `${inset}px`,
      left: `${inset}px`,
    },
    duration: animate && motionAllowed() ? TRANSITION_MS : 0,
    ease,
  })
  return true
}
// On the very first layout the pane may not be measured yet; frame once it is.
let framePending = false
onNodesInitialized(() => {
  if (framePending && frameGraph(false)) framePending = false
})
// The minimap only earns its space once part of the graph is off screen;
// the strip under the glass top bar counts as off screen.
const everythingVisible = computed(() => {
  const devices = getNodes.value.filter((node) => node.type === 'device')
  if (!devices.length || !dimensions.value.width) return true
  const { x, y, zoom } = viewport.value
  const bounds = getRectOfNodes(devices)
  const left = -x / zoom
  const top = (topbarHeight() - y) / zoom
  const right = (dimensions.value.width - x) / zoom
  const bottom = (dimensions.value.height - y) / zoom
  return (
    bounds.x >= left &&
    bounds.y >= top &&
    bounds.x + bounds.width <= right &&
    bounds.y + bounds.height <= bottom
  )
})

interface Placement {
  x: number
  y: number
  width?: number
  height?: number
}
function placementOf(node: Node): Placement {
  const size = node.style && typeof node.style === 'object' ? node.style : {}
  return {
    x: node.position.x,
    y: node.position.y,
    width: 'width' in size ? parseFloat(String(size.width)) : undefined,
    height: 'height' in size ? parseFloat(String(size.height)) : undefined,
  }
}
// Nodes that survive a relayout glide from where they were to where the new
// layout puts them; edges follow because they read live node positions.
let tween = 0
function glide(from: Map<string, Placement>, to: Map<string, Placement>) {
  const started = performance.now()
  const id = ++tween
  const step = (now: number) => {
    if (id !== tween) return
    const t = ease(Math.min(1, (now - started) / TRANSITION_MS))
    for (const [nodeId, end] of to) {
      const start = from.get(nodeId)
      const node = findNode(nodeId)
      if (!start || !node) continue
      node.position = {
        x: start.x + (end.x - start.x) * t,
        y: start.y + (end.y - start.y) * t,
      }
      if (end.width !== undefined && end.height !== undefined)
        node.style = {
          width: `${(start.width ?? end.width) + (end.width - (start.width ?? end.width)) * t}px`,
          height: `${(start.height ?? end.height) + (end.height - (start.height ?? end.height)) * t}px`,
        }
    }
    if (t < 1) requestAnimationFrame(step)
  }
  requestAnimationFrame(step)
}

let run = 0
async function relayout() {
  const current = ++run
  try {
    const box = frame.value?.getBoundingClientRect()
    const laid = await layoutTopology(
      props.sites,
      members.value,
      links.value,
      box?.height ? box.width / box.height : 2.4,
    )
    if (current !== run) return
    const previous = new Map(
      getNodes.value.map((node) => [node.id, placementOf(node)]),
    )
    const animate = previous.size > 0 && motionAllowed()
    const targets = new Map(
      laid.nodes.map((node) => [node.id, placementOf(node)]),
    )
    // Surviving nodes start where they were; the glide moves them on.
    const placed: Node[] = animate
      ? laid.nodes.map((node): Node => {
          const start = previous.get(node.id)
          if (!start) return node
          const moved: Node = { ...node, position: { x: start.x, y: start.y } }
          if (start.width !== undefined && start.height !== undefined)
            moved.style = {
              width: `${start.width}px`,
              height: `${start.height}px`,
            }
          return moved
        })
      : laid.nodes
    nodes.value = placed
    edges.value = laid.edges
    failed.value = false
    const sites = laid.nodes.filter((node) => node.type === 'site')
    framed = sites.length
      ? sites.reduce<Rect>(
          (box, node, index) => {
            const size = placementOf(node)
            const rect = {
              x: node.position.x,
              y: node.position.y,
              width: size.width ?? 0,
              height: size.height ?? 0,
            }
            if (!index) return rect
            const x = Math.min(box.x, rect.x)
            const y = Math.min(box.y, rect.y)
            return {
              x,
              y,
              width: Math.max(box.x + box.width, rect.x + rect.width) - x,
              height: Math.max(box.y + box.height, rect.y + rect.height) - y,
            }
          },
          { x: 0, y: 0, width: 0, height: 0 },
        )
      : undefined
    await nextTick()
    if (animate) glide(previous, targets)
    framePending = !frameGraph(animate)
  } catch {
    failed.value = true
  }
}
watch(structure, relayout)
// A resized canvas (window, sidebar) re-packs the sites for its new shape.
let resizeTimer: ReturnType<typeof setTimeout> | undefined
let observer: ResizeObserver | undefined
onMounted(() => {
  void relayout()
  let first = true
  observer = new ResizeObserver(() => {
    if (first) {
      first = false
      return
    }
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => void relayout(), 160)
  })
  if (frame.value) observer.observe(frame.value)
})
onUnmounted(() => {
  observer?.disconnect()
  clearTimeout(resizeTimer)
  tween++
})
watch(
  () => props.focus,
  (focus) => {
    const [deviceId, port] = (focus ?? '').split('~')
    if (deviceId && port) select({ kind: 'port', id: deviceId, port })
    else if (deviceId) select({ kind: 'device', id: deviceId })
  },
  { immediate: true },
)
watch(members, () => {
  if (
    selection.value &&
    !devicesById.value.has(selection.value.id) &&
    !linksById.value.has(selection.value.id)
  )
    selection.value = undefined
})

const history = ref<Record<string, number[]>>({})
watch(
  () => [
    ...members.value.map((device) => [device.id, device.throughput] as const),
    ...links.value.map((link) => [link.id, link.throughput] as const),
  ],
  (samples) => {
    const next: Record<string, number[]> = {}
    for (const [id, value] of samples)
      next[id] = [...(history.value[id] ?? []), value].slice(-SAMPLES)
    history.value = next
  },
  { immediate: true },
)

function nodesChanged(changes: NodeChange[]) {
  for (const change of changes)
    if (change.type === 'select' && change.selected)
      select({ kind: 'device', id: change.id })
}
function edgesChanged(changes: EdgeChange[]) {
  for (const change of changes)
    if (change.type === 'select' && change.selected)
      select({ kind: 'link', id: change.id })
}
</script>

<template>
  <div ref="frame" class="topology-graph">
    <VueFlow
      :id="flowId"
      :nodes="nodes"
      :edges="edges"
      :min-zoom="0.2"
      :max-zoom="1.6"
      :nodes-connectable="false"
      :edges-updatable="false"
      :zoom-on-double-click="false"
      @nodes-change="nodesChanged"
      @edges-change="edgesChanged"
      @pane-click="select(undefined)"
      @edge-mouse-enter="hovered = $event.edge.id"
      @edge-mouse-leave="hovered = undefined"
    >
      <template #node-device="nodeProps">
        <TopologyNode v-bind="nodeProps" />
      </template>
      <template #node-site="nodeProps">
        <TopologySiteNode
          v-bind="nodeProps"
          :sites="sites"
          :tenant-name="tenantName"
        />
      </template>
      <template #edge-link="edgeProps">
        <TopologyLink v-bind="edgeProps" />
      </template>
      <Background :gap="24" :size="1.4" pattern-color="var(--input)" />
      <Controls
        :show-interactive="false"
        :show-fit-view="false"
        position="bottom-left"
      >
        <UiTooltip label="Fit to view" side="right">
          <ControlButton aria-label="Fit view" @click="frameGraph(true)">
            <AppIcon name="expand" />
          </ControlButton>
        </UiTooltip>
      </Controls>
      <MiniMap
        :class="{ 'minimap-hidden': everythingVisible }"
        :aria-hidden="everythingVisible"
        pannable
        zoomable
        position="bottom-right"
        :node-color="
          (node: Node) =>
            node.type === 'site'
              ? 'transparent'
              : `var(--${devicesById.get(node.id)?.health === 'Healthy' ? 'graph-edge' : 'primary'})`
        "
        mask-color="color-mix(in srgb, var(--background) 70%, transparent)"
      />
    </VueFlow>
    <p class="topology-assumption">
      <i aria-hidden="true"></i>
      Assumed link, not yet discovered
    </p>
    <TopologyInspector :history="history" :site-name="siteName" />
    <p v-if="failed" class="topology-error" role="alert">
      The topology could not be laid out. Reload to try again.
    </p>
  </div>
</template>

<style scoped>
.topology-graph {
  position: relative;
  height: 100%;
  background: var(--background);
}
.topology-graph :deep(.vue-flow__background) {
  color: var(--input);
}
.topology-graph :deep(.topology-inspector) {
  top: calc(var(--topbar-height) + 14px);
  max-height: calc(100% - var(--topbar-height) - 28px);
}
.topology-graph :deep(.vue-flow__node) {
  cursor: pointer;
  animation: topology-node-in 260ms ease both;
}
@keyframes topology-node-in {
  from {
    opacity: 0;
  }
}
@media (prefers-reduced-motion: reduce) {
  .topology-graph :deep(.vue-flow__node) {
    animation: none;
  }
}
.topology-graph :deep(.vue-flow__node-site) {
  z-index: -1 !important;
  cursor: grab;
}
.topology-graph :deep(.vue-flow__handle) {
  width: 1px;
  min-width: 0;
  height: 1px;
  min-height: 0;
  border: 0;
  background: transparent;
}
.topology-graph :deep(.vue-flow__controls) {
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  box-shadow: none;
}
.topology-graph :deep(.vue-flow__controls-button) {
  border-bottom: 1px solid var(--border);
  background: var(--card);
  color: var(--foreground);
  fill: currentColor;
}
.topology-graph :deep(.vue-flow__controls-button:hover) {
  background: var(--hover);
}
.topology-graph :deep(.vue-flow__minimap) {
  transition:
    opacity 180ms ease,
    visibility 180ms;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--card);
}
.topology-graph :deep(.vue-flow__minimap.minimap-hidden) {
  visibility: hidden;
  opacity: 0;
  pointer-events: none;
}
.topology-assumption {
  position: absolute;
  z-index: 2;
  bottom: 14px;
  left: 50%;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 9px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--card);
  color: var(--muted-foreground);
  font-size: var(--text-sm);
  transform: translateX(-50%);
  pointer-events: none;
}
.topology-assumption i {
  width: 24px;
  border-top: 2px dashed var(--graph-edge);
}
@media (prefers-reduced-motion: reduce) {
  .topology-graph :deep(.vue-flow__minimap) {
    transition: none;
  }
}
.topology-error {
  position: absolute;
  inset: auto 16px 16px;
  font-size: var(--text-sm);
  color: var(--danger-foreground);
}
</style>
