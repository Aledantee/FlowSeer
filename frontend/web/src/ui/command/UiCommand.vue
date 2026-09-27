<script setup lang="ts">
import { ComboboxRoot } from 'reka-ui'

export interface UiCommandProps {
  modelValue?: string
  highlightedValue?: string
  ignoreFilter?: boolean
}

withDefaults(defineProps<UiCommandProps>(), {
  modelValue: '',
  highlightedValue: '',
  ignoreFilter: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'update:highlightedValue', value: string): void
  (e: 'highlight', value: string): void
}>()

function onHighlight(item: unknown) {
  let val = ''
  if (item && typeof item === 'object' && 'value' in item) {
    val = String((item as { value: unknown }).value ?? '')
  } else if (typeof item === 'string') {
    val = item
  }
  emit('update:highlightedValue', val)
  emit('update:modelValue', val)
  emit('highlight', val)
}
</script>

<template>
  <ComboboxRoot
    :open="true"
    :model-value="modelValue"
    :ignore-filter="ignoreFilter"
    class="bg-popover text-foreground flex h-full w-full flex-col overflow-hidden rounded-panel"
    @update:model-value="emit('update:modelValue', $event as string)"
    @highlight="onHighlight"
  >
    <slot />
  </ComboboxRoot>
</template>
