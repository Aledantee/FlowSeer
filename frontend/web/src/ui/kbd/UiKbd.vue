<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

const props = withDefaults(defineProps<UiAiProps>(), {
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
  <kbd
    ref="anchor"
    class="inline-flex items-center justify-center font-mono text-2xs px-1.5 py-0.5 rounded-sm border border-border bg-subtle text-muted-foreground shadow-xs"
  >
    <slot />
  </kbd>
</template>
