<script setup lang="ts">
import { ComboboxGroup, ComboboxLabel } from 'reka-ui'

export interface UiCommandGroupProps {
  heading?: string
}

defineProps<UiCommandGroupProps>()
</script>

<template>
  <ComboboxGroup
    class="overflow-hidden p-1 text-foreground [&_[data-reka-combobox-label]]:px-2 [&_[data-reka-combobox-label]]:py-1.5 [&_[data-reka-combobox-label]]:text-xs [&_[data-reka-combobox-label]]:font-medium [&_[data-reka-combobox-label]]:text-muted-foreground"
  >
    <ComboboxLabel v-if="heading" as="div">
      {{ heading }}
    </ComboboxLabel>
    <slot />
  </ComboboxGroup>
</template>
