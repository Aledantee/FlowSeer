<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { tv } from 'tailwind-variants'
import UiBadge from './UiBadge.vue'

export interface UiStatusBadgeProps {
  status: 'Healthy' | 'Degraded' | 'Offline'
  label?: string
  size?: 'sm' | 'md'
}

const props = withDefaults(defineProps<UiStatusBadgeProps>(), {
  label: undefined,
  size: 'md',
})

const { t } = useI18n({ useScope: 'global' })

const statusMessageKeyMap = {
  Healthy: 'ui.statusBadge.healthy',
  Degraded: 'ui.statusBadge.degraded',
  Offline: 'ui.statusBadge.offline',
} as const

const displayLabel = computed(
  () => props.label ?? t(statusMessageKeyMap[props.status]),
)

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
  <UiBadge
    class="!text-sm"
    :variant="variantMap[props.status]"
    :size="props.size"
  >
    <i aria-hidden="true" :class="dotVariants({ status })" />
    <slot>{{ displayLabel }}</slot>
  </UiBadge>
</template>
