<script setup lang="ts">
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

export interface UiPaginationProps {
  total: number
  itemsPerPage?: number
  page?: number
  defaultPage?: number
  siblingCount?: number
  showEdges?: boolean
}

withDefaults(defineProps<UiPaginationProps>(), {
  itemsPerPage: 10,
  page: undefined,
  defaultPage: 1,
  siblingCount: 1,
  showEdges: false,
})

const emit = defineEmits<{
  (e: 'update:page', page: number): void
}>()
</script>

<template>
  <PaginationRoot
    :total="total"
    :items-per-page="itemsPerPage"
    :page="page"
    :default-page="defaultPage"
    :sibling-count="siblingCount"
    :show-edges="showEdges"
    class="flex items-center gap-2"
    @update:page="emit('update:page', $event)"
  >
    <PaginationList v-slot="{ items }" class="flex items-center gap-1 text-xs">
      <PaginationFirst
        v-if="showEdges"
        class="inline-flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        aria-label="First page"
      >
        <span aria-hidden="true">«</span>
      </PaginationFirst>

      <PaginationPrev
        class="inline-flex h-8 px-2.5 items-center justify-center gap-1 rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        aria-label="Previous page"
      >
        <span aria-hidden="true">‹</span>
        <span>Previous</span>
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
          >
            {{ item.value }}
          </button>
        </PaginationListItem>
        <PaginationEllipsis
          v-else
          :key="item.type"
          :index="index"
          class="flex h-8 w-8 items-center justify-center text-muted-foreground"
        >
          &#8230;
        </PaginationEllipsis>
      </template>

      <PaginationNext
        class="inline-flex h-8 px-2.5 items-center justify-center gap-1 rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        aria-label="Next page"
      >
        <span>Next</span>
        <span aria-hidden="true">›</span>
      </PaginationNext>

      <PaginationLast
        v-if="showEdges"
        class="inline-flex h-8 w-8 items-center justify-center rounded-control border border-border bg-card text-foreground hover:bg-hover disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        aria-label="Last page"
      >
        <span aria-hidden="true">»</span>
      </PaginationLast>
    </PaginationList>
  </PaginationRoot>
</template>
