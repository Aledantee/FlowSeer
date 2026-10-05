<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { SwitchRoot, SwitchThumb } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'
import { useFormReset } from './useFormReset'

export interface UiSwitchProps extends UiAiProps {
  modelValue?: boolean
  name?: string
  required?: boolean
  disabled?: boolean
  id?: string
  value?: string
}

const props = withDefaults(defineProps<UiSwitchProps>(), {
  modelValue: undefined,
  name: undefined,
  required: false,
  disabled: false,
  id: undefined,
  value: 'on',
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<
  UiAiEmits & {
    (e: 'update:modelValue', value: boolean): void
  }
>()

const rootRef = ref<{ $el?: unknown } | null>(null)
useAiTarget(rootRef, () => props.ai)
useAiOrigin(
  rootRef,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
const internalValue = ref<boolean>(props.modelValue ?? false)

watch(
  () => props.modelValue,
  (val) => {
    internalValue.value = val ?? false
  },
)

const currentValue = computed<boolean>(() =>
  props.modelValue !== undefined ? props.modelValue : internalValue.value,
)

let initialValue = false
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

function handleUpdate(val: boolean) {
  internalValue.value = val
  emit('update:modelValue', val)
}
</script>

<template>
  <SwitchRoot
    :id="id"
    ref="rootRef"
    :model-value="currentValue"
    :name="name"
    :required="required"
    :disabled="disabled"
    :value="value"
    class="peer inline-flex h-5 w-9 shrink-0 !p-0 cursor-pointer items-center !rounded-full !border-2 !border-transparent !bg-input transition-colors focus-visible:!outline-none focus-visible:!ring-1 focus-visible:!ring-ring disabled:!cursor-not-allowed disabled:!opacity-50 data-[state=checked]:!bg-primary"
    @update:model-value="handleUpdate"
  >
    <SwitchThumb
      class="pointer-events-none block h-4 w-4 rounded-full bg-background shadow-xs ring-0 transition-transform data-[state=checked]:translate-x-4 data-[state=unchecked]:translate-x-0"
    />
  </SwitchRoot>
</template>
