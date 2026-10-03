<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  BaseEdge,
  EdgeLabelRenderer,
  Position,
  getSmoothStepPath,
} from '@vue-flow/core'
import { linkDetailsOf } from '../../domain/telemetry'
import { aiTarget, useAiSlot } from '../../ai'
import type { AiTarget } from '../../ai'
import { useFormat } from '../../i18n/format'
import { useLabels } from '../../i18n/labels'
import { useTopologyLive } from './live'
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  id: string
  sourceX: number
  sourceY: number
  targetX: number
  targetY: number
  sourcePosition: Position
  targetPosition: Position
  data: { linkId: string }
}>()
const { t } = useI18n({ useScope: 'global' })
const format = useFormat()
const labels = useLabels()
const live = useTopologyLive()
const link = computed(() => live.link(props.data.linkId))
const selected = computed(
  () =>
    live.selection.value?.kind === 'link' &&
    live.selection.value.id === props.data.linkId,
)
const showPorts = computed(
  () => selected.value || live.hovered.value === props.id,
)
function hover(on: boolean) {
  if (on) live.hovered.value = props.id
  else if (live.hovered.value === props.id) live.hovered.value = undefined
}
const details = computed(() =>
  link.value ? linkDetailsOf(live.fleet.value, link.value) : undefined,
)
const ends = computed(() =>
  link.value && details.value
    ? [
        {
          side: 'source',
          deviceId: link.value.sourceId,
          port: details.value.sourcePort,
          y: drop.value.top + 3,
        },
        {
          side: 'target',
          deviceId: link.value.targetId,
          port: details.value.targetPort,
          y: drop.value.bottom - 15,
        },
      ]
    : [],
)
const speed = computed(() =>
  link.value ? format.speed(link.value.capacity) : '',
)
// The same sentence the legend shows, then the link's ends and reading.
const ariaLabel = computed(() => {
  const current = link.value
  if (!current) return ''
  return t('view.topology.linkLabel', {
    assumption: t('view.topology.assumption'),
    source: details.value?.sourcePort?.name ?? '',
    target: details.value?.targetPort?.name ?? '',
    speed: speed.value,
    throughput: format.rate(current.throughput),
    health: labels.health(current.health),
  })
})
// A downed link reads as the port state it shows at both ends.
const reading = computed(() => {
  const current = link.value
  if (!current) return ''
  return format.facts([
    speed.value,
    current.health === 'Offline'
      ? labels.portStatus('Down')
      : format.rate(current.throughput),
  ])
})
const slot = useAiSlot()
const target = computed<AiTarget | undefined>(() => {
  const current = link.value
  if (!current) return undefined
  return aiTarget({
    slot,
    view: 'topology',
    kind: 'link',
    entityId: current.id,
    label: t('view.topology.linkTarget', { speed: speed.value }),
    context: {
      health: current.health,
      capacity: speed.value,
      throughput: format.rate(current.throughput),
      source:
        live.device(current.sourceId)?.name ?? t('view.common.unknownDevice'),
      target:
        live.device(current.targetId)?.name ?? t('view.common.unknownDevice'),
      sourcePort: details.value?.sourcePort?.name ?? '',
      targetPort: details.value?.targetPort?.name ?? '',
    },
  })
})
// Each link owns the vertical drop from the shared bus down to its target;
// its labels sit there so siblings under one switch never collide.
const drop = computed(() => ({
  x: props.targetX,
  top: (props.sourceY + props.targetY) / 2,
  bottom: props.targetY,
}))
const geometry = computed(() =>
  getSmoothStepPath({
    sourceX: props.sourceX,
    sourceY: props.sourceY,
    sourcePosition: props.sourcePosition,
    targetX: props.targetX,
    targetY: props.targetY,
    targetPosition: props.targetPosition,
    borderRadius: 10,
  }),
)
// Width grows with the share of capacity in use, so busy links stand out
// without a legend.
const width = computed(() => {
  const share = link.value ? link.value.throughput / link.value.capacity : 0
  return 1.5 + Math.min(1, share * 8) * 3
})
</script>

<template>
  <g
    v-if="link"
    :class="['topology-link', link.health.toLowerCase(), { selected }]"
  >
    <BaseEdge
      :id="id"
      :path="geometry[0]"
      :interaction-width="18"
      :style="{ strokeWidth: width }"
    />
    <path
      v-if="link.throughput"
      class="topology-link-flow"
      :d="geometry[0]"
      :style="{ strokeWidth: width }"
    />
  </g>
  <EdgeLabelRenderer v-if="link">
    <button
      v-ai-target="target"
      :class="[
        'topology-link-label',
        'nodrag',
        'nopan',
        link.health.toLowerCase(),
        { selected },
      ]"
      :style="{
        transform: `translate(calc(-100% - 6px), -50%) translate(${drop.x}px, ${(drop.top + drop.bottom) / 2}px)`,
      }"
      :aria-label="ariaLabel"
      @click="live.select({ kind: 'link', id: link.id })"
      @mouseenter="hover(true)"
      @mouseleave="hover(false)"
    >
      {{ reading }}
    </button>
    <template v-for="end in ends" :key="end.side">
      <button
        v-if="end.port"
        :class="[
          'topology-port-label',
          'nodrag',
          'nopan',
          { visible: showPorts },
        ]"
        :style="{ transform: `translate(${drop.x + 6}px, ${end.y}px)` }"
        :tabindex="showPorts ? 0 : -1"
        :aria-hidden="!showPorts"
        translate="no"
        :aria-label="t('view.topology.portLabel', { name: end.port.name })"
        @mouseenter="hover(true)"
        @mouseleave="hover(false)"
        @click="
          live.select({ kind: 'port', id: end.deviceId, port: end.port.name })
        "
      >
        {{ end.port.name }}
      </button>
    </template>
  </EdgeLabelRenderer>
</template>

<style scoped>
.topology-link :deep(.vue-flow__edge-path) {
  stroke: var(--graph-edge);
  stroke-dasharray: 4 5;
  transition: stroke 120ms ease;
}
.topology-link.degraded :deep(.vue-flow__edge-path) {
  stroke: var(--warning-border);
}
.topology-link.offline :deep(.vue-flow__edge-path) {
  stroke: var(--danger-border);
}
.topology-link.selected :deep(.vue-flow__edge-path),
.topology-link:hover :deep(.vue-flow__edge-path) {
  stroke: var(--accent-foreground);
}
.topology-link-flow {
  fill: none;
  stroke: var(--chart-1);
  stroke-dasharray: 3 14;
  stroke-linecap: round;
  opacity: 0.7;
  pointer-events: none;
  animation: topology-flow 1.1s linear infinite;
}
@keyframes topology-flow {
  to {
    stroke-dashoffset: -17;
  }
}
@media (prefers-reduced-motion: reduce) {
  .topology-link-flow {
    animation: none;
  }
}
.topology-link-label {
  position: absolute;
  z-index: 1;
  padding: 2px 7px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--card);
  color: var(--muted-foreground);
  font-size: var(--text-2xs);
  font-variant-numeric: tabular-nums;
  pointer-events: all;
  cursor: pointer;
}
.topology-link-label:hover,
.topology-link-label.selected {
  border-color: var(--accent-foreground);
  color: var(--accent-foreground);
}
.topology-link-label.degraded {
  border-color: var(--warning-border);
  color: var(--warning-foreground);
}
.topology-link-label.offline {
  border-color: var(--danger-border);
  color: var(--danger-foreground);
}
.topology-port-label {
  position: absolute;
  z-index: 1;
  padding: 1px 4px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--muted-foreground);
  font-family: var(--font-mono);
  font-size: var(--text-2xs);
  white-space: nowrap;
  opacity: 0;
  visibility: hidden;
  cursor: pointer;
  transition:
    opacity 120ms ease,
    visibility 120ms;
  pointer-events: all;
}
.topology-port-label.visible {
  opacity: 1;
  visibility: visible;
}
.topology-port-label:hover {
  border-color: var(--border);
  background: var(--card);
  color: var(--accent-foreground);
}
</style>
