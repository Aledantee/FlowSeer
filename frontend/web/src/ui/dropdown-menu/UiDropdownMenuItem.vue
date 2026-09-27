<script setup lang="ts">
import { DropdownMenuItem } from 'reka-ui'

export interface UiDropdownMenuItemProps {
  disabled?: boolean
  textValue?: string
  asChild?: boolean
}

withDefaults(defineProps<UiDropdownMenuItemProps>(), {
  disabled: false,
  textValue: undefined,
  asChild: false,
})

const emit = defineEmits<{
  (e: 'select', event: Event): void
}>()
</script>

<template>
  <DropdownMenuItem
    :disabled="disabled"
    :text-value="textValue"
    :as-child="asChild"
    class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
    @select="emit('select', $event)"
  >
    <slot />
  </DropdownMenuItem>
</template>
