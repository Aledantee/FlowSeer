<script setup lang="ts">
import { computed, inject, useTemplateRef, type Ref } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiTableCellProps extends UiAiProps {
  align?: 'left' | 'center' | 'right' | 'numeric'
  mono?: boolean
}

const props = withDefaults(defineProps<UiTableCellProps>(), {
  align: 'left',
  mono: false,
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

const tableContext = inject<{
  dense: Ref<boolean>
  stickyHeader: Ref<boolean>
} | null>('ui-table-context', null)

const isDense = computed(() => tableContext?.dense.value ?? false)

const alignmentClass = computed(() => {
  if (props.align === 'numeric' || props.align === 'right') return 'text-right'
  if (props.align === 'center') return 'text-center'
  return 'text-left'
})

const paddingClass = computed(() =>
  isDense.value ? 'py-2 px-3' : 'py-3.5 px-4',
)

const typographyClass = computed(() => {
  if (props.mono || props.align === 'numeric') {
    return 'font-mono tabular-nums text-xs'
  }
  return ''
})
</script>

<template>
  <td
    ref="anchor"
    :class="['align-middle', alignmentClass, paddingClass, typographyClass]"
  >
    <slot />
  </td>
</template>
