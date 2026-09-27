<script setup lang="ts">
export interface UiTableRowProps {
  selected?: boolean
}

withDefaults(defineProps<UiTableRowProps>(), {
  selected: false,
})
</script>

<template>
  <tr
    :data-state="selected ? 'selected' : undefined"
    class="border-b border-border transition-colors hover:bg-hover data-[state=selected]:bg-subtle"
  >
    <slot />
  </tr>
</template>
