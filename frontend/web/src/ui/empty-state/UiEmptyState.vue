<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiEmptyStateProps extends UiAiProps {
  title: string
  description?: string
}

const props = withDefaults(defineProps<UiEmptyStateProps>(), {
  description: undefined,
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
</script>

<template>
  <div
    ref="anchor"
    class="flex flex-col items-center justify-center p-8 text-center text-muted-foreground gap-3"
  >
    <div v-if="$slots.icon" class="text-muted-foreground">
      <slot name="icon" />
    </div>
    <div class="space-y-1">
      <h3 class="text-sm font-semibold text-foreground">
        <slot name="title">{{ title }}</slot>
      </h3>
      <p
        v-if="description || $slots.description"
        class="text-xs text-muted-foreground max-w-sm"
      >
        <slot name="description">{{ description }}</slot>
      </p>
    </div>
    <div v-if="$slots.actions" class="flex items-center gap-2 mt-2">
      <slot name="actions" />
    </div>
    <slot />
  </div>
</template>
