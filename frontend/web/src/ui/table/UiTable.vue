<script setup lang="ts">
import { provide, toRef, useTemplateRef, type Ref } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiTableProps extends UiAiProps {
  dense?: boolean
  stickyHeader?: boolean
}

export interface TableContext {
  dense: Ref<boolean>
  stickyHeader: Ref<boolean>
}

const props = withDefaults(defineProps<UiTableProps>(), {
  dense: false,
  stickyHeader: false,
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

provide<TableContext>('ui-table-context', {
  dense: toRef(props, 'dense'),
  stickyHeader: toRef(props, 'stickyHeader'),
})
</script>

<template>
  <table
    ref="anchor"
    data-ui-table
    class="w-full caption-bottom text-sm border-collapse text-left"
  >
    <slot />
  </table>
</template>
