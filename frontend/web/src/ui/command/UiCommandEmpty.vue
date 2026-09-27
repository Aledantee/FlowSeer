<script setup lang="ts">
import { ComboboxEmpty } from 'reka-ui'
</script>

<template>
  <ComboboxEmpty class="py-6 text-center text-sm text-muted-foreground">
    <slot>No results found.</slot>
  </ComboboxEmpty>
</template>
