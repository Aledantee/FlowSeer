<script setup lang="ts">
export interface UiBreadcrumbSeparatorProps {
  as?: string
}

withDefaults(defineProps<UiBreadcrumbSeparatorProps>(), {
  as: 'li',
})
</script>

<template>
  <component
    :is="as"
    role="presentation"
    aria-hidden="true"
    class="text-muted-foreground select-none"
  >
    <slot>/</slot>
  </component>
</template>
