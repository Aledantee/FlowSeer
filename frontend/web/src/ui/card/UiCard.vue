<script setup lang="ts">
// UiCard container component
</script>

<template>
  <div
    class="bg-card border border-border rounded-panel shadow-xs p-4 flex flex-col gap-3"
  >
    <header v-if="$slots.header" class="flex items-center justify-between">
      <slot name="header" />
    </header>
    <div v-if="$slots.default" class="flex-1">
      <slot />
    </div>
    <footer v-if="$slots.footer" class="mt-auto pt-2 border-t border-border">
      <slot name="footer" />
    </footer>
  </div>
</template>
