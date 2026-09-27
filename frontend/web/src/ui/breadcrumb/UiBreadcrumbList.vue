<script setup lang="ts">
export interface UiBreadcrumbListProps {
  as?: string
}

withDefaults(defineProps<UiBreadcrumbListProps>(), {
  as: 'ol',
})
</script>

<template>
  <component
    :is="as"
    class="flex items-center gap-1.5 text-sm text-muted-foreground"
  >
    <slot />
  </component>
</template>
