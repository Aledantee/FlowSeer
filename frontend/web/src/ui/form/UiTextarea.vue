<script setup lang="ts">
import { computed, inject, useAttrs, type Ref } from 'vue'

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
  modelValue: '',
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

const textareaId = computed(() => props.id ?? fieldContext?.id.value)
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

function handleInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLTextAreaElement).value)
}
</script>

<template>
  <textarea
    :id="textareaId"
    :value="modelValue"
    :name="name"
    :rows="rows"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :required="required"
    :aria-label="computedAriaLabel"
    :aria-invalid="isInvalid ? 'true' : undefined"
    :aria-describedby="ariaDescribedBy"
    :class="[
      'bg-card border border-input rounded-control text-sm px-3 py-1.5 text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:border-ring disabled:opacity-50 disabled:cursor-not-allowed w-full min-h-[80px] transition-colors',
      {
        'border-danger-border ring-danger-border focus-visible:ring-danger-border focus-visible:border-danger-border':
          isInvalid,
      },
    ]"
    @input="handleInput"
  />
</template>
