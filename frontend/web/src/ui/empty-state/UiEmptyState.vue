<script setup lang="ts">
export interface UiEmptyStateProps {
  title: string
  description?: string
}

defineProps<UiEmptyStateProps>()
</script>

<template>
  <div
    class="flex flex-col items-center justify-center p-8 text-center text-muted-foreground gap-3"
  >
    <div v-if="$slots.icon" class="text-muted-foreground">
      <slot name="icon" />
    </div>
    <div class="space-y-1">
      <h3 class="text-sm font-semibold text-foreground">
        <slot name="title">{{ title }}</slot>
      </h3>
      <p
        v-if="description || $slots.description"
        class="text-xs text-muted-foreground max-w-sm"
      >
        <slot name="description">{{ description }}</slot>
      </p>
    </div>
    <div v-if="$slots.actions" class="flex items-center gap-2 mt-2">
      <slot name="actions" />
    </div>
    <slot />
  </div>
</template>
