<script setup lang="ts">
import { provide, toRef, type Ref } from 'vue'

export interface UiTableProps {
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
})

provide<TableContext>('ui-table-context', {
  dense: toRef(props, 'dense'),
  stickyHeader: toRef(props, 'stickyHeader'),
})
</script>

<template>
  <table
    data-ui-table
    class="w-full caption-bottom text-sm border-collapse text-left"
  >
    <slot />
  </table>
</template>
