<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ values: number[]; label: string }>()
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
</script>

<template>
  <svg
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
    />
    <path v-if="values.length > 1" :d="line" class="sparkline-line" />
  </svg>
</template>
