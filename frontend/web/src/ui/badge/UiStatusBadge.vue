<script setup lang="ts">
import { tv } from 'tailwind-variants'
import UiBadge from './UiBadge.vue'

export interface UiStatusBadgeProps {
  status: 'Healthy' | 'Degraded' | 'Offline'
  size?: 'sm' | 'md'
}

const props = withDefaults(defineProps<UiStatusBadgeProps>(), {
  size: 'md',
})

const variantMap = {
  Healthy: 'success',
  Degraded: 'warning',
  Offline: 'danger',
} as const

const dotVariants = tv({
  base: 'h-1.5 w-1.5 rounded-full bg-current shrink-0',
  variants: {
    status: {
      Healthy: '',
      Degraded: 'motion-safe:animate-pulse',
      Offline: 'motion-safe:animate-pulse',
    },
  },
  defaultVariants: {
    status: 'Healthy',
  },
})
</script>

<template>
  <UiBadge :variant="variantMap[props.status]" :size="props.size">
    <i aria-hidden="true" :class="dotVariants({ status })" />
    <slot>{{ status }}</slot>
  </UiBadge>
</template>
