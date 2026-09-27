<script setup lang="ts">
export interface UiTableEmptyProps {
  colSpan: number
}

defineProps<UiTableEmptyProps>()
</script>

<template>
  <tr>
    <td :colspan="colSpan" class="p-8 text-center text-muted-foreground">
      <slot />
    </td>
  </tr>
</template>
