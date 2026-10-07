<script setup lang="ts">
import { computed, inject, useTemplateRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiTableHeadProps extends UiAiProps {
  sortable?: boolean
  sortDirection?: 'ascending' | 'descending' | 'none'
  align?: 'left' | 'center' | 'right' | 'numeric'
  ascendingMark?: string
  descendingMark?: string
}

const props = withDefaults(defineProps<UiTableHeadProps>(), {
  sortable: false,
  sortDirection: undefined,
  align: 'left',
  ascendingMark: undefined,
  descendingMark: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits & { (e: 'sort'): void }>()

const anchor = useTemplateRef('anchor')
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

const { t } = useI18n({ useScope: 'global' })

const resolvedAscendingMark = computed(
  () => props.ascendingMark ?? t('ui.tableHead.ascendingMark'),
)
const resolvedDescendingMark = computed(
  () => props.descendingMark ?? t('ui.tableHead.descendingMark'),
)

const tableContext = inject<{
  dense: Ref<boolean>
  stickyHeader: Ref<boolean>
} | null>('ui-table-context', null)

const isDense = computed(() => tableContext?.dense.value ?? false)
const isSticky = computed(() => tableContext?.stickyHeader.value ?? false)

const alignmentClass = computed(() => {
  if (props.align === 'numeric' || props.align === 'right') return 'text-right'
  if (props.align === 'center') return 'text-center'
  return 'text-left'
})

const buttonAlignmentClass = computed(() => {
  if (props.align === 'numeric' || props.align === 'right') return 'justify-end'
  if (props.align === 'center') return 'justify-center'
  return 'justify-start'
})

const paddingClass = computed(() => (isDense.value ? 'py-2 px-3' : 'py-3 px-4'))

const stickyClass = computed(() =>
  isSticky.value
    ? 'sticky top-0 z-(--z-sticky) bg-subtle backdrop-blur-xs'
    : '',
)

const ariaSortValue = computed(() => {
  if (!props.sortable) return undefined
  return props.sortDirection ?? 'none'
})

function handleClick() {
  if (props.sortable) {
    emit('sort')
  }
}
</script>

<template>
  <th
    ref="anchor"
    :aria-sort="ariaSortValue"
    :class="[
      'font-medium text-xs text-muted-foreground align-middle',
      alignmentClass,
      paddingClass,
      stickyClass,
    ]"
    @click="handleClick"
  >
    <button
      v-if="sortable"
      type="button"
      :class="[
        'inline-flex items-center gap-1 font-medium text-xs text-muted-foreground hover:text-foreground cursor-pointer',
        buttonAlignmentClass,
      ]"
      @click.stop="handleClick"
    >
      <slot />
      <span v-if="sortDirection === 'ascending'" aria-hidden="true">{{
        resolvedAscendingMark
      }}</span>
      <span v-else-if="sortDirection === 'descending'" aria-hidden="true">{{
        resolvedDescendingMark
      }}</span>
    </button>
    <slot v-else />
  </th>
</template>
