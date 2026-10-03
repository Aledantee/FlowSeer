<script setup lang="ts">
import { computed } from 'vue'
import {
  PaginationEllipsis,
  PaginationFirst,
  PaginationLast,
  PaginationList,
  PaginationListItem,
  PaginationNext,
  PaginationPrev,
  PaginationRoot,
} from 'reka-ui'
import { useI18n } from 'vue-i18n'

export interface UiPaginationProps {
  total: number
  itemsPerPage?: number
  page?: number
  defaultPage?: number
  siblingCount?: number
  showEdges?: boolean
  firstLabel?: string
  previousLabel?: string
  nextLabel?: string
  lastLabel?: string
  previousText?: string
  nextText?: string
  pageLabel?: (page: number) => string
  firstMark?: string
  previousMark?: string
  nextMark?: string
  lastMark?: string
  ellipsis?: string
}

const props = withDefaults(defineProps<UiPaginationProps>(), {
  itemsPerPage: 10,
  page: undefined,
  defaultPage: 1,
  siblingCount: 1,
  showEdges: false,
  firstLabel: undefined,
  previousLabel: undefined,
  nextLabel: undefined,
  lastLabel: undefined,
  previousText: undefined,
  nextText: undefined,
  pageLabel: undefined,
  firstMark: undefined,
  previousMark: undefined,
  nextMark: undefined,
  lastMark: undefined,
  ellipsis: undefined,
})

const emit = defineEmits<{
  (e: 'update:page', page: number): void
}>()

const { t, n } = useI18n({ useScope: 'global' })

const resolvedFirstLabel = computed(
  () => props.firstLabel ?? t('ui.pagination.firstLabel'),
)
const resolvedPreviousLabel = computed(
  () => props.previousLabel ?? t('ui.pagination.previousLabel'),
)
const resolvedNextLabel = computed(
  () => props.nextLabel ?? t('ui.pagination.nextLabel'),
)
const resolvedLastLabel = computed(
  () => props.lastLabel ?? t('ui.pagination.lastLabel'),
)
const resolvedPreviousText = computed(
  () => props.previousText ?? t('ui.pagination.previousText'),
)
const resolvedNextText = computed(
  () => props.nextText ?? t('ui.pagination.nextText'),
)
const resolvedFirstMark = computed(
  () => props.firstMark ?? t('ui.pagination.firstMark'),
)
const resolvedPreviousMark = computed(
  () => props.previousMark ?? t('ui.pagination.previousMark'),
)
const resolvedNextMark = computed(
  () => props.nextMark ?? t('ui.pagination.nextMark'),
)
const resolvedLastMark = computed(
  () => props.lastMark ?? t('ui.pagination.lastMark'),
)
const resolvedEllipsis = computed(
  () => props.ellipsis ?? t('ui.pagination.ellipsis'),
)

function resolvePageLabel(pageNumber: number): string {
  if (props.pageLabel) {
    return props.pageLabel(pageNumber)
  }
  return t('ui.pagination.pageLabel', { page: n(pageNumber, 'decimal') })
}
</script>

<template>
  <PaginationRoot
    :total="total"
    :items-per-page="itemsPerPage"
    :page="page"
    :default-page="defaultPage"
    :sibling-count="siblingCount"
    :show-edges="showEdges"
    class="flex flex-wrap items-center gap-2 max-w-full"
    @update:page="emit('update:page', $event)"
  >
    <PaginationList
      v-slot="{ items }"
      class="flex flex-wrap items-center gap-1 text-xs max-w-full"
    >
      <PaginationFirst
        v-if="showEdges"
        class="inline-flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        :aria-label="resolvedFirstLabel"
      >
        <span aria-hidden="true">{{ resolvedFirstMark }}</span>
      </PaginationFirst>

      <PaginationPrev
        class="inline-flex h-8 px-2.5 items-center justify-center gap-1 rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        :aria-label="resolvedPreviousLabel"
      >
        <span aria-hidden="true">{{ resolvedPreviousMark }}</span>
        <span>{{ resolvedPreviousText }}</span>
      </PaginationPrev>

      <template v-for="(item, index) in items" :key="index">
        <PaginationListItem
          v-if="item.type === 'page'"
          :value="item.value"
          as-child
        >
          <button
            type="button"
            class="inline-flex h-8 min-w-8 px-2 items-center justify-center rounded-control font-medium transition-colors border border-transparent hover:bg-hover cursor-pointer data-[selected]:bg-primary data-[selected]:text-primary-foreground data-[selected]:border-primary"
            :aria-label="resolvePageLabel(item.value)"
          >
            {{ n(item.value, 'decimal') }}
          </button>
        </PaginationListItem>
        <PaginationEllipsis
          v-else
          :key="item.type"
          :index="index"
          class="flex h-8 w-8 items-center justify-center text-muted-foreground"
        >
          {{ resolvedEllipsis }}
        </PaginationEllipsis>
      </template>

      <PaginationNext
        class="inline-flex h-8 px-2.5 items-center justify-center gap-1 rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        :aria-label="resolvedNextLabel"
      >
        <span>{{ resolvedNextText }}</span>
        <span aria-hidden="true">{{ resolvedNextMark }}</span>
      </PaginationNext>

      <PaginationLast
        v-if="showEdges"
        class="inline-flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        :aria-label="resolvedLastLabel"
      >
        <span aria-hidden="true">{{ resolvedLastMark }}</span>
      </PaginationLast>
    </PaginationList>
  </PaginationRoot>
</template>
