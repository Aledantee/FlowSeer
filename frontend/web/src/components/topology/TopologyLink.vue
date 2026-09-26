<script setup lang="ts">
import { computed } from 'vue'
import {
  BaseEdge,
  EdgeLabelRenderer,
  Position,
  getSmoothStepPath,
} from '@vue-flow/core'
import { linkDetailsOf } from '../../domain/telemetry'
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
  link.value ? `${link.value.capacity / 1000}G` : '',
)
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
      :aria-label="`Link ${details?.sourcePort?.name ?? ''} to ${details?.targetPort?.name ?? ''}, ${speed}, ${link.throughput} Mbps, ${link.health}`"
      @click="live.select({ kind: 'link', id: link.id })"
      @mouseenter="hover(true)"
      @mouseleave="hover(false)"
    >
      {{
        link.health === 'Offline'
          ? `${speed} · Down`
          : `${speed} · ${link.throughput} Mbps`
      }}
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
        :aria-label="`Port ${end.port.name}`"
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
