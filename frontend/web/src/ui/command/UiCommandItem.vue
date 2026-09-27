<script setup lang="ts">
import { ComboboxItem } from 'reka-ui'

export interface UiCommandItemProps {
  value: string
  disabled?: boolean
}

defineProps<UiCommandItemProps>()

const emit = defineEmits<{
  (e: 'select', value: string): void
}>()
</script>

<template>
  <ComboboxItem
    :value="value"
    :disabled="disabled"
    class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
    @select="emit('select', value)"
  >
    <slot />
  </ComboboxItem>
</template>
