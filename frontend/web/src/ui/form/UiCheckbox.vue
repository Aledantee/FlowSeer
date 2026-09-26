<script setup lang="ts">
import { CheckboxIndicator, CheckboxRoot } from 'reka-ui'

export interface UiCheckboxProps {
  modelValue?: boolean | 'indeterminate'
  name?: string
  required?: boolean
  disabled?: boolean
  id?: string
  value?: string
}

withDefaults(defineProps<UiCheckboxProps>(), {
  modelValue: false,
  name: undefined,
  required: false,
  disabled: false,
  id: undefined,
  value: 'on',
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean | 'indeterminate'): void
}>()
</script>

<template>
  <CheckboxRoot
    :id="id"
    :model-value="modelValue"
    :name="name"
    :required="required"
    :disabled="disabled"
    :value="value"
    class="peer h-4 w-4 shrink-0 rounded-sm border border-input bg-card text-primary-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=checked]:border-primary data-[state=indeterminate]:bg-primary data-[state=indeterminate]:border-primary transition-colors flex items-center justify-center"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <CheckboxIndicator
      class="flex items-center justify-center text-primary-foreground"
    >
      <svg
        v-if="modelValue === 'indeterminate'"
        class="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="3"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <line x1="5" y1="12" x2="19" y2="12" />
      </svg>
      <svg
        v-else
        class="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="3"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <polyline points="20 6 9 17 4 12" />
      </svg>
    </CheckboxIndicator>
  </CheckboxRoot>
</template>
