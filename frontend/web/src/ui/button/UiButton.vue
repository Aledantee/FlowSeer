<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { tv } from 'tailwind-variants'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'
import UiSpinner from '../spinner/UiSpinner.vue'

export interface UiButtonProps extends UiAiProps {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'sm' | 'md' | 'icon'
  type?: 'button' | 'submit' | 'reset'
  disabled?: boolean
  loading?: boolean
  ariaLabel?: string
}

const props = withDefaults(defineProps<UiButtonProps>(), {
  variant: 'secondary',
  size: 'md',
  type: 'button',
  disabled: false,
  loading: false,
  ariaLabel: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const button = useTemplateRef('button')
useAiTarget(button, () => props.ai)
useAiOrigin(
  button,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

const buttonVariants = tv({
  base: 'inline-flex items-center justify-center gap-1.5 font-medium !rounded-control transition-colors focus-visible:!outline-none focus-visible:!ring-1 focus-visible:!ring-ring disabled:!opacity-50 disabled:!cursor-not-allowed select-none text-center',
  variants: {
    variant: {
      primary:
        '!bg-primary !text-primary-foreground hover:brightness-105 !border-transparent',
      secondary:
        '!bg-card !text-foreground !border !border-border hover:!bg-hover',
      ghost:
        '!bg-transparent !text-foreground !border-transparent hover:!bg-hover',
      danger:
        '!bg-danger-surface !text-danger-foreground !border !border-danger-border hover:brightness-95',
    },
    size: {
      sm: 'min-h-7 h-auto !px-2.5 !py-1 text-xs',
      md: 'min-h-8 h-auto !px-3.5 !py-1.5 text-sm',
      icon: 'h-8 w-8 !p-0 shrink-0',
    },
  },
  defaultVariants: {
    variant: 'secondary',
    size: 'md',
  },
})

const computedAriaLabel = computed(() => {
  const label = props.ariaLabel
  if (props.size === 'icon' && !label) {
    console.warn(
      '[UiButton] An accessible aria-label is required when size="icon"',
    )
  }
  return label
})

function handleClick(event: MouseEvent) {
  if (props.disabled || props.loading) {
    event.preventDefault()
    event.stopImmediatePropagation()
  }
}
</script>

<template>
  <button
    ref="button"
    :type="type"
    :disabled="disabled || loading"
    :aria-busy="loading ? 'true' : undefined"
    :aria-label="computedAriaLabel"
    :class="buttonVariants({ variant, size })"
    @click.capture="handleClick"
  >
    <UiSpinner v-if="loading" :size="size === 'sm' ? 'sm' : 'md'" />
    <slot v-if="!(loading && size === 'icon')" />
  </button>
</template>
