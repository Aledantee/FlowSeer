<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { ProgressIndicator, ProgressRoot } from 'reka-ui'
import { useI18n } from 'vue-i18n'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiProgressProps extends UiAiProps {
  modelValue?: number | null
  max?: number
  size?: 'sm' | 'md' | 'lg'
  variant?: 'default' | 'accent' | 'success' | 'warning' | 'danger'
  ariaLabel?: string
  valueText?:
    | ((value: number | null | undefined, max: number) => string | undefined)
    | string
}

const props = withDefaults(defineProps<UiProgressProps>(), {
  modelValue: undefined,
  max: 100,
  size: 'md',
  variant: 'default',
  ariaLabel: undefined,
  valueText: undefined,
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

const { n, t } = useI18n({ useScope: 'global' })
const resolvedAriaLabel = computed(
  () => props.ariaLabel ?? t('ui.progress.ariaLabel'),
)

function resolvedGetValueText(
  value: number | null | undefined,
  maxVal: number,
): string | undefined {
  if (typeof props.valueText === 'function') {
    return props.valueText(value, maxVal)
  }
  if (typeof props.valueText === 'string') {
    return props.valueText
  }
  if (typeof value !== 'number' || maxVal <= 0) {
    return undefined
  }
  return n(value / maxVal, 'percent')
}

const sizeClass = computed(() => {
  switch (props.size) {
    case 'sm':
      return 'h-1'
    case 'lg':
      return 'h-3'
    case 'md':
    default:
      return 'h-2'
  }
})

const variantClass = computed(() => {
  switch (props.variant) {
    case 'accent':
      return 'bg-accent'
    case 'success':
      return 'bg-success-foreground'
    case 'warning':
      return 'bg-warning-foreground'
    case 'danger':
      return 'bg-danger-foreground'
    case 'default':
    default:
      return 'bg-primary'
  }
})

const isIndeterminate = computed(() => props.modelValue == null)

const indicatorStyle = computed(() => {
  if (props.modelValue == null) {
    return {}
  }
  const val = Math.min(props.max, Math.max(0, props.modelValue))
  const pct = (val / props.max) * 100
  return {
    transform: `translateX(-${100 - pct}%)`,
  }
})
</script>

<template>
  <ProgressRoot
    ref="anchor"
    :model-value="modelValue"
    :max="max"
    :aria-label="resolvedAriaLabel"
    :get-value-label="() => resolvedAriaLabel"
    :get-value-text="resolvedGetValueText"
    :class="[
      'relative overflow-hidden rounded-full bg-subtle w-full',
      sizeClass,
    ]"
  >
    <ProgressIndicator
      :class="[
        'h-full w-full transition-transform duration-300 ease-out motion-reduce:animate-none',
        isIndeterminate && 'animate-progress-slide',
        variantClass,
      ]"
      :style="indicatorStyle"
    />
  </ProgressRoot>
</template>

<style>
@keyframes progress-slide {
  0% {
    transform: translateX(-100%);
    opacity: 0.6;
  }
  50% {
    transform: translateX(0%);
    opacity: 1;
  }
  100% {
    transform: translateX(100%);
    opacity: 0.6;
  }
}

.animate-progress-slide {
  animation: progress-slide 1.5s ease-in-out infinite;
}

@media (prefers-reduced-motion: reduce) {
  .animate-progress-slide {
    animation: none;
  }
}
</style>
