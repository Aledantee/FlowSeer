<script setup lang="ts">
// UiKbd renders keyboard shortcut keys
</script>

<template>
  <kbd
    class="inline-flex items-center justify-center font-mono text-2xs px-1.5 py-0.5 rounded border border-border bg-subtle text-muted-foreground shadow-xs"
  >
    <slot />
  </kbd>
</template>
