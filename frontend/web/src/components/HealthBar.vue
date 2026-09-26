<script setup lang="ts">
import { computed } from 'vue'
import type { Health } from '../domain/fleet'
const props = defineProps<{
  counts: Record<Health, number>
  legend?: boolean
}>()
const order: Health[] = ['Healthy', 'Degraded', 'Offline']
const total = computed(() =>
  order.reduce((sum, health) => sum + props.counts[health], 0),
)
const summary = computed(() =>
  order.map((health) => `${props.counts[health]} ${health}`).join(', '),
)
</script>

<template>
  <div class="health-bar">
    <div class="health-track" role="img" :aria-label="summary">
      <template v-for="health in order" :key="health">
        <span
          v-if="counts[health]"
          :class="['health-segment', health.toLowerCase()]"
          :style="{ flexGrow: counts[health] }"
          :title="`${counts[health]} ${health}`"
        ></span>
      </template>
      <span v-if="!total" class="health-segment empty"></span>
    </div>
    <ul v-if="legend" class="health-legend">
      <li v-for="health in order" :key="health">
        <i :class="['health-key', health.toLowerCase()]" aria-hidden="true"></i
        >{{ health }}<strong>{{ counts[health] }}</strong>
      </li>
    </ul>
  </div>
</template>
