<script setup lang="ts">
import { computed, inject, onMounted, ref, useAttrs, type Ref } from 'vue'
import { tv } from 'tailwind-variants'
import { useFormReset } from './useFormReset'

export interface UiTextareaProps {
  modelValue?: string
  id?: string
  placeholder?: string
  disabled?: boolean
  readonly?: boolean
  required?: boolean
  name?: string
  rows?: number
  invalid?: boolean
  ariaLabel?: string
}

const props = withDefaults(defineProps<UiTextareaProps>(), {
  modelValue: undefined,
  id: undefined,
  placeholder: undefined,
  disabled: false,
  readonly: false,
  required: false,
  name: undefined,
  rows: 3,
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

const textareaRef = ref<HTMLTextAreaElement | null>(null)
const textareaId = computed(() => props.id ?? fieldContext?.id.value)
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

const textareaVariants = tv({
  base: 'w-full min-h-[80px] text-sm !px-3 !py-1.5 !rounded-control transition-colors !bg-card !text-foreground placeholder:text-muted-foreground disabled:!opacity-50 disabled:!cursor-not-allowed focus-visible:!outline-none !border',
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

let initialValue = ''
onMounted(() => {
  initialValue =
    props.modelValue !== undefined
      ? props.modelValue
      : (textareaRef.value?.value ?? '')
})

useFormReset({
  elementRef: textareaRef,
  onReset: () => {
    if (textareaRef.value) {
      textareaRef.value.value = String(initialValue ?? '')
    }
    emit('update:modelValue', String(initialValue ?? ''))
  },
})

function handleInput(event: Event) {
  if (event.target instanceof HTMLTextAreaElement) {
    emit('update:modelValue', event.target.value)
  }
}
</script>

<template>
  <textarea
    :id="textareaId"
    ref="textareaRef"
    v-bind="valueBinding"
    :name="name"
    :rows="rows"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :required="required"
    :aria-label="computedAriaLabel"
    :aria-invalid="isInvalid ? 'true' : undefined"
    :aria-describedby="ariaDescribedBy"
    :class="textareaVariants({ invalid: isInvalid })"
    @input="handleInput"
  />
</template>
