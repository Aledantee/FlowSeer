<script setup lang="ts">
import { computed, inject, onMounted, ref, useAttrs, type Ref } from 'vue'
import { tv } from 'tailwind-variants'
import { useFormReset } from './useFormReset'

export interface UiInputProps {
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
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const attrs = useAttrs()

const fieldContext = inject<{
  id: Ref<string>
  describedBy: Ref<string | undefined>
  invalid: Ref<boolean>
} | null>('ui-field-context', null)

const inputRef = ref<HTMLInputElement | null>(null)
const inputId = computed(() => props.id ?? fieldContext?.id.value)
const isInvalid = computed(
  () => props.invalid ?? fieldContext?.invalid.value ?? false,
)
const computedAriaLabel = computed(
  () => props.ariaLabel ?? (attrs['aria-label'] as string | undefined),
)
const ariaDescribedBy = computed(
  () =>
    (attrs['aria-describedby'] as string | undefined) ??
    fieldContext?.describedBy.value,
)

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
  emit('update:modelValue', (event.target as HTMLInputElement).value)
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
