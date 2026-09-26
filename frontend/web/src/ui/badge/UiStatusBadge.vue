<script setup lang="ts">
import UiBadge from './UiBadge.vue'

const props = withDefaults(
  defineProps<{
    status: 'Healthy' | 'Degraded' | 'Offline'
    size?: 'sm' | 'md'
  }>(),
  { size: 'md' },
)

const variantMap = {
  Healthy: 'success',
  Degraded: 'warning',
  Offline: 'danger',
} as const
</script>

<template>
  <UiBadge :variant="variantMap[props.status]" :size="props.size">
    <i
      aria-hidden="true"
      :class="[
        'h-1.5 w-1.5 rounded-full bg-current shrink-0',
        { 'animate-pulse': status === 'Degraded' || status === 'Offline' },
      ]"
    />
    <slot>{{ status }}</slot>
  </UiBadge>
</template>
