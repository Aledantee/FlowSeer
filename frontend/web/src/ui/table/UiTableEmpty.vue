<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiTableEmptyProps extends UiAiProps {
  colSpan: number
}

const props = withDefaults(defineProps<UiTableEmptyProps>(), {
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
  <tr ref="anchor">
    <td :colspan="colSpan" class="p-8 text-center text-muted-foreground">
      <slot />
    </td>
  </tr>
</template>
