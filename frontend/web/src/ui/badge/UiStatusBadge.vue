<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { tv } from 'tailwind-variants'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'
import UiBadge from './UiBadge.vue'

export interface UiStatusBadgeProps extends UiAiProps {
  status: 'Healthy' | 'Degraded' | 'Offline'
  label?: string
  size?: 'sm' | 'md'
}

const props = withDefaults(defineProps<UiStatusBadgeProps>(), {
  label: undefined,
  size: 'md',
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
    ref="anchor"
    class="!text-sm max-w-full"
    :variant="variantMap[props.status]"
    :size="props.size"
    :title="$slots.default ? undefined : displayLabel"
  >
    <i aria-hidden="true" :class="dotVariants({ status })" />
    <span class="truncate">
      <slot>{{ displayLabel }}</slot>
    </span>
  </UiBadge>
</template>
