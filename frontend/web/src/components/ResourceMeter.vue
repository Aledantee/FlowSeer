<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ label: string; percent: number; detail?: string }>()
// Thresholds follow common NMS defaults: warn at 75 %, critical at 90 %.
const tone = computed(() =>
  props.percent >= 90 ? 'critical' : props.percent >= 75 ? 'warning' : 'normal',
)
</script>

<template>
  <div :class="['resource-meter', tone]">
    <span class="resource-meter-label">{{ label }}</span>
    <span class="resource-meter-value"
      >{{ percent }} %<small v-if="detail"> · {{ detail }}</small></span
    >
    <span
      class="resource-meter-track"
      role="meter"
      :aria-label="label"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="percent"
      ><i :style="{ width: `${percent}%` }"></i
    ></span>
  </div>
</template>
