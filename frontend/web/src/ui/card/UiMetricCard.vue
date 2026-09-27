<script setup lang="ts">
import UiCard from './UiCard.vue'

export interface UiMetricCardProps {
  label: string
  value: number
  unit?: string
}

defineProps<UiMetricCardProps>()
</script>

<template>
  <UiCard as="article" class="metric-card">
    <div
      class="flex items-center justify-between text-xs text-muted-foreground font-medium"
    >
      <span>{{ label }}</span>
      <span v-if="$slots.icon" class="metric-icon">
        <slot name="icon" />
      </span>
    </div>
    <div class="flex items-baseline gap-1 mt-1">
      <span
        class="font-mono text-2xl font-semibold tabular-nums text-foreground metric-value"
      >
        {{ value }}
      </span>
      <span v-if="unit" class="text-xs text-muted-foreground font-mono">
        {{ unit }}
      </span>
    </div>
    <div v-if="$slots.default" class="text-xs text-muted-foreground mt-1">
      <slot />
    </div>
  </UiCard>
</template>
