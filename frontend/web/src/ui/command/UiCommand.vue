<script setup lang="ts">
import { ComboboxRoot } from 'reka-ui'

export interface UiCommandProps {
  modelValue?: string
  ignoreFilter?: boolean
}

withDefaults(defineProps<UiCommandProps>(), {
  modelValue: '',
  ignoreFilter: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>

<template>
  <ComboboxRoot
    :open="true"
    :model-value="modelValue"
    :ignore-filter="ignoreFilter"
    class="bg-popover text-foreground flex h-full w-full flex-col overflow-hidden rounded-panel"
    @update:model-value="emit('update:modelValue', $event as string)"
  >
    <slot />
  </ComboboxRoot>
</template>
