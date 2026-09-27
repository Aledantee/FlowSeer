<script setup lang="ts">
import {
  computed,
  inject,
  onMounted,
  ref,
  useAttrs,
  watch,
  type Ref,
} from 'vue'
import { tv } from 'tailwind-variants'
import {
  SelectContent,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectViewport,
} from 'reka-ui'
import { useFormReset } from './useFormReset'

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

export interface UiSelectProps {
  modelValue?: string
  options?: SelectOption[]
  placeholder?: string
  disabled?: boolean
  name?: string
  required?: boolean
  id?: string
  invalid?: boolean
  ariaLabel?: string
}

const props = withDefaults(defineProps<UiSelectProps>(), {
  modelValue: undefined,
  options: () => [],
  placeholder: 'Select an option...',
  disabled: false,
  name: undefined,
  required: false,
  id: undefined,
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

const triggerRef = ref<InstanceType<typeof SelectTrigger> | null>(null)
const triggerId = computed(() => props.id ?? fieldContext?.id.value)
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

const selectTriggerVariants = tv({
  base: 'w-full inline-flex items-center justify-between gap-2 text-sm !px-3 !py-1.5 !rounded-control transition-colors !bg-card !text-foreground disabled:!opacity-50 disabled:!cursor-not-allowed focus-visible:!outline-none !border',
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

const isControlled = computed(() => props.modelValue !== undefined)
const internalValue = ref<string>(props.modelValue ?? '')

watch(
  () => props.modelValue,
  (val) => {
    if (val !== undefined) {
      internalValue.value = val
    }
  },
)

const currentValue = computed(() =>
  isControlled.value ? (props.modelValue as string) : internalValue.value,
)

let initialValue = ''
onMounted(() => {
  initialValue =
    props.modelValue !== undefined ? props.modelValue : internalValue.value
})

useFormReset({
  elementRef: triggerRef,
  onReset: () => {
    internalValue.value = initialValue
    emit('update:modelValue', initialValue)
  },
})

function handleUpdate(val: string | null | undefined) {
  const strVal = String(val ?? '')
  internalValue.value = strVal
  emit('update:modelValue', strVal)
}
</script>

<template>
  <SelectRoot
    :model-value="currentValue"
    :disabled="disabled"
    :required="required"
    :name="name"
    @update:model-value="handleUpdate"
  >
    <SelectTrigger
      :id="triggerId"
      ref="triggerRef"
      :aria-label="computedAriaLabel"
      :aria-invalid="isInvalid ? 'true' : undefined"
      :aria-describedby="ariaDescribedBy"
      :class="selectTriggerVariants({ invalid: isInvalid })"
    >
      <SelectValue :placeholder="placeholder" />
      <svg
        class="h-4 w-4 opacity-50 shrink-0"
        xmlns="http://www.w3.org/2000/svg"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </SelectTrigger>

    <SelectPortal>
      <SelectContent
        class="bg-popover border border-border shadow-md rounded-control p-1 z-50 min-w-[8rem] text-foreground max-h-60 overflow-y-auto"
        position="popper"
        :side-offset="4"
      >
        <SelectViewport class="p-1">
          <SelectItem
            v-for="opt in options"
            :key="opt.value"
            :value="opt.value"
            :disabled="opt.disabled"
            class="relative flex w-full cursor-pointer select-none items-center rounded-sm py-1.5 pl-8 pr-2 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
          >
            <span
              class="absolute left-2 flex h-3.5 w-3.5 items-center justify-center text-primary"
            >
              <SelectItemIndicator>
                <svg
                  class="h-4 w-4"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </SelectItemIndicator>
            </span>
            <SelectItemText>{{ opt.label }}</SelectItemText>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
