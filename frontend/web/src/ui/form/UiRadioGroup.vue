<script setup lang="ts">
import { computed, onMounted, ref, useId, watch } from 'vue'
import { tv } from 'tailwind-variants'
import { RadioGroupIndicator, RadioGroupItem, RadioGroupRoot } from 'reka-ui'
import { useFormReset } from './useFormReset'

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

const props = withDefaults(defineProps<UiRadioGroupProps>(), {
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

const instanceId = useId()
const getOptionId = (val: string) => `${instanceId}-${val}`

const radioLabelVariants = tv({
  base: 'text-sm font-medium !text-foreground select-none',
  variants: {
    disabled: {
      false: 'cursor-pointer',
      true: '!opacity-50 !cursor-not-allowed',
    },
  },
  defaultVariants: {
    disabled: false,
  },
})

const rootRef = ref<{ $el?: unknown } | null>(null)
const internalValue = ref<string>(props.modelValue ?? '')

watch(
  () => props.modelValue,
  (val) => {
    internalValue.value = val ?? ''
  },
)

const currentValue = computed<string>(() =>
  props.modelValue !== undefined ? props.modelValue : internalValue.value,
)

let initialValue = ''
onMounted(() => {
  initialValue =
    props.modelValue !== undefined ? props.modelValue : internalValue.value
})

useFormReset({
  elementRef: rootRef,
  onReset: () => {
    internalValue.value = initialValue
    emit('update:modelValue', initialValue)
  },
})

function handleUpdate(val: unknown) {
  const strVal = String(val ?? '')
  internalValue.value = strVal
  emit('update:modelValue', strVal)
}
</script>

<template>
  <RadioGroupRoot
    ref="rootRef"
    :model-value="currentValue"
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
    @update:model-value="handleUpdate"
  >
    <div
      v-for="opt in options"
      :key="opt.value"
      class="inline-flex items-center gap-2"
    >
      <RadioGroupItem
        :id="getOptionId(opt.value)"
        :value="opt.value"
        :disabled="opt.disabled || disabled"
        class="h-4 w-4 shrink-0 !p-0 !rounded-full !border !border-input !bg-card !text-primary focus-visible:!outline-none focus-visible:!ring-1 focus-visible:!ring-ring disabled:!cursor-not-allowed disabled:!opacity-50 data-[state=checked]:!border-primary flex items-center justify-center"
      >
        <RadioGroupIndicator
          class="flex items-center justify-center w-full h-full relative after:content-[''] after:block after:w-2 after:h-2 after:rounded-full after:bg-primary"
        />
      </RadioGroupItem>
      <label
        :for="getOptionId(opt.value)"
        :class="radioLabelVariants({ disabled: opt.disabled || disabled })"
      >
        {{ opt.label }}
      </label>
    </div>
  </RadioGroupRoot>
</template>
