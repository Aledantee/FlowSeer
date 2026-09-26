<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

export interface UiSwitchProps {
  modelValue?: boolean
  name?: string
  required?: boolean
  disabled?: boolean
  id?: string
  value?: string
}

withDefaults(defineProps<UiSwitchProps>(), {
  modelValue: false,
  name: undefined,
  required: false,
  disabled: false,
  id: undefined,
  value: 'on',
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()
</script>

<template>
  <SwitchRoot
    :id="id"
    :model-value="modelValue"
    :name="name"
    :required="required"
    :disabled="disabled"
    :value="value"
    class="peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent bg-input transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <SwitchThumb
      class="pointer-events-none block h-4 w-4 rounded-full bg-background shadow-xs ring-0 transition-transform data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0"
    />
  </SwitchRoot>
</template>
