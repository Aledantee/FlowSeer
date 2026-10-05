<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ui/ai/context'
import { useAiOrigin } from '../ui/ai/useAiOrigin'
import { useAiTarget } from '../ui/ai/useAiTarget'

export type ChartColor =
  'chart-1' | 'chart-2' | 'chart-3' | 'chart-4' | 'chart-5' | 'chart-6'

export interface TrafficSparklineProps extends UiAiProps {
  values: number[]
  label: string
  color?: ChartColor
}

const props = withDefaults(defineProps<TrafficSparklineProps>(), {
  color: 'chart-1',
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const anchor = useTemplateRef('anchor')
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

const WIDTH = 240
const HEIGHT = 44
const line = computed(() => {
  // Headroom keeps a steady series off the top edge.
  const max = Math.max(1, ...props.values) * 1.25
  const step = WIDTH / Math.max(1, props.values.length - 1)
  return props.values
    .map(
      (value, index) =>
        `${index ? 'L' : 'M'}${(index * step).toFixed(1)},${(HEIGHT - 2 - (value / max) * (HEIGHT - 4)).toFixed(1)}`,
    )
    .join('')
})

const strokeColor = computed(() => `var(--${props.color})`)
const fillColor = computed(
  () => `color-mix(in srgb, var(--${props.color}) 14%, transparent)`,
)
</script>

<template>
  <svg
    ref="anchor"
    class="sparkline"
    :viewBox="`0 0 ${WIDTH} ${HEIGHT}`"
    preserveAspectRatio="none"
    role="img"
    :aria-label="label"
  >
    <path
      v-if="values.length > 1"
      :d="`${line}L${WIDTH},${HEIGHT}L0,${HEIGHT}Z`"
      class="sparkline-area"
      :style="{ fill: fillColor }"
    />
    <path
      v-if="values.length > 1"
      :d="line"
      class="sparkline-line"
      :style="{ stroke: strokeColor }"
    />
  </svg>
</template>

<style scoped>
.sparkline {
  display: block;
  width: 100%;
  height: 44px;
}

.sparkline-line {
  fill: none;
  stroke-width: 1.5;
  vector-effect: non-scaling-stroke;
}
</style>
