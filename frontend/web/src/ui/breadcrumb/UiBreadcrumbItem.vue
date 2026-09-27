<script setup lang="ts">
export interface UiBreadcrumbItemProps {
  as?: string
}

withDefaults(defineProps<UiBreadcrumbItemProps>(), {
  as: 'li',
})
</script>

<template>
  <component :is="as" class="inline-flex items-center gap-1.5">
    <slot />
  </component>
</template>
