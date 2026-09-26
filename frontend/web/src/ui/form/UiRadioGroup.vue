<script setup lang="ts">
import { RadioGroupIndicator, RadioGroupItem, RadioGroupRoot } from 'reka-ui'

export interface RadioOption {
  value: string
  label: string
  disabled?: boolean
}

export interface UiRadioGroupProps {
  modelValue?: string
  options?: RadioOption[]
  orientation?: 'horizontal' | 'vertical'
  name?: string
  disabled?: boolean
  required?: boolean
}

withDefaults(defineProps<UiRadioGroupProps>(), {
  modelValue: undefined,
  options: () => [],
  orientation: 'vertical',
  name: undefined,
  disabled: false,
  required: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>

<template>
  <RadioGroupRoot
    :model-value="modelValue"
    :orientation="orientation"
    :name="name"
    :disabled="disabled"
    :required="required"
    :class="[
      'gap-3',
      orientation === 'horizontal'
        ? 'flex flex-row items-center'
        : 'flex flex-col',
    ]"
    @update:model-value="(val) => emit('update:modelValue', String(val ?? ''))"
  >
    <div
      v-for="opt in options"
      :key="opt.value"
      class="inline-flex items-center gap-2"
    >
      <RadioGroupItem
        :id="opt.value"
        :value="opt.value"
        :disabled="opt.disabled || disabled"
        class="h-4 w-4 shrink-0 rounded-full border border-input bg-card text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:border-primary flex items-center justify-center"
      >
        <RadioGroupIndicator
          class="flex items-center justify-center w-full h-full relative after:content-[''] after:block after:w-2 after:h-2 after:rounded-full after:bg-primary"
        />
      </RadioGroupItem>
      <label
        :for="opt.value"
        :class="[
          'text-sm font-medium text-foreground select-none cursor-pointer',
          { 'opacity-50 cursor-not-allowed': opt.disabled || disabled },
        ]"
      >
        {{ opt.label }}
      </label>
    </div>
  </RadioGroupRoot>
</template>
