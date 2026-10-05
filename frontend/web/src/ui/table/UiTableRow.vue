<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiTableRowProps extends UiAiProps {
  selected?: boolean
}

const props = withDefaults(defineProps<UiTableRowProps>(), {
  selected: false,
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
  <tr
    ref="anchor"
    :data-state="selected ? 'selected' : undefined"
    class="border-b border-border transition-colors hover:bg-hover data-[state=selected]:bg-subtle"
  >
    <slot />
  </tr>
</template>
