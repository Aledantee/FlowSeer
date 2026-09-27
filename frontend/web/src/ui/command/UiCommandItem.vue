<script setup lang="ts">
import type { ComboboxItemEmits } from 'reka-ui'
import { ComboboxItem } from 'reka-ui'

export interface UiCommandItemProps {
  value: string
  disabled?: boolean
}

const props = defineProps<UiCommandItemProps>()

type RekaSelectEvent = ComboboxItemEmits<string>['select'][0]

export interface UiCommandItemSelectEvent {
  value: string
  altKey: boolean
  ctrlKey: boolean
  metaKey: boolean
  shiftKey: boolean
}

const emit = defineEmits<{
  (e: 'select', event: UiCommandItemSelectEvent): void
}>()

function select(event: RekaSelectEvent) {
  const { originalEvent } = event.detail
  emit('select', {
    value: props.value,
    altKey: originalEvent.altKey,
    ctrlKey: originalEvent.ctrlKey,
    metaKey: originalEvent.metaKey,
    shiftKey: originalEvent.shiftKey,
  })
}
</script>

<template>
  <ComboboxItem
    :value="value"
    :disabled="disabled"
    :data-command-value="value"
    class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
    @select="select"
  >
    <slot />
  </ComboboxItem>
</template>
