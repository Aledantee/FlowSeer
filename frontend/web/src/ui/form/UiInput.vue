<script setup lang="ts">
import { computed, inject, onMounted, ref, useAttrs, type Ref } from 'vue'
import { tv } from 'tailwind-variants'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'
import { useFormReset } from './useFormReset'

export interface UiInputProps extends UiAiProps {
  modelValue?: string | number
  id?: string
  type?: string
  placeholder?: string
  disabled?: boolean
  readonly?: boolean
  required?: boolean
  name?: string
  invalid?: boolean
  ariaLabel?: string
}

const props = withDefaults(defineProps<UiInputProps>(), {
  modelValue: undefined,
  id: undefined,
  type: 'text',
  placeholder: undefined,
  disabled: false,
  readonly: false,
  required: false,
  name: undefined,
  invalid: undefined,
  ariaLabel: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<
  UiAiEmits & {
    (e: 'update:modelValue', value: string): void
  }
>()

const attrs = useAttrs()

const fieldContext = inject<{
  id: Ref<string>
  describedBy: Ref<string | undefined>
  invalid: Ref<boolean>
} | null>('ui-field-context', null)

const inputRef = ref<HTMLInputElement | null>(null)
useAiTarget(inputRef, () => props.ai)
useAiOrigin(
  inputRef,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
const inputId = computed(() => props.id ?? fieldContext?.id.value)
const isInvalid = computed(
  () => props.invalid ?? fieldContext?.invalid.value ?? false,
)
const computedAriaLabel = computed(() => {
  if (props.ariaLabel !== undefined) return props.ariaLabel
  const attr = attrs['aria-label']
  return typeof attr === 'string' ? attr : undefined
})
const ariaDescribedBy = computed(() => {
  const attr = attrs['aria-describedby']
  if (typeof attr === 'string') return attr
  return fieldContext?.describedBy.value
})

const inputVariants = tv({
  base: 'w-full text-sm !px-3 !py-1.5 !rounded-control transition-colors !bg-card !text-foreground placeholder:text-muted-foreground disabled:!opacity-50 disabled:!cursor-not-allowed focus-visible:!outline-none !border',
  variants: {
    invalid: {
      false:
        '!border-input focus-visible:!ring-1 focus-visible:!ring-ring focus-visible:!border-ring',
      true: '!border-danger-border !ring-danger-border focus-visible:!ring-danger-border focus-visible:!border-danger-border',
    },
  },
  defaultVariants: {
    invalid: false,
  },
})

const valueBinding = computed(() =>
  props.modelValue !== undefined ? { value: props.modelValue } : {},
)

let initialValue: string | number = ''
onMounted(() => {
  initialValue =
    props.modelValue !== undefined
      ? props.modelValue
      : (inputRef.value?.value ?? '')
})

useFormReset({
  elementRef: inputRef,
  onReset: () => {
    if (inputRef.value) {
      inputRef.value.value = String(initialValue ?? '')
    }
    emit('update:modelValue', String(initialValue ?? ''))
  },
})

function handleInput(event: Event) {
  if (event.target instanceof HTMLInputElement) {
    emit('update:modelValue', event.target.value)
  }
}
</script>

<template>
  <input
    :id="inputId"
    ref="inputRef"
    :type="type"
    v-bind="valueBinding"
    :name="name"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :required="required"
    :aria-label="computedAriaLabel"
    :aria-invalid="isInvalid ? 'true' : undefined"
    :aria-describedby="ariaDescribedBy"
    :class="inputVariants({ invalid: isInvalid })"
    @input="handleInput"
  />
</template>
