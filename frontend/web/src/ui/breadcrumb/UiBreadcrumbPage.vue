<script setup lang="ts">
export interface UiBreadcrumbPageProps {
  as?: string
}

withDefaults(defineProps<UiBreadcrumbPageProps>(), {
  as: 'span',
})
</script>

<template>
  <component
    :is="as"
    role="link"
    aria-disabled="true"
    aria-current="page"
    class="font-medium text-foreground"
  >
    <slot />
  </component>
</template>
