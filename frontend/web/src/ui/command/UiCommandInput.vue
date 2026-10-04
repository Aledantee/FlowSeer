<script setup lang="ts">
import { computed } from 'vue'
import { ComboboxInput, injectComboboxRootContext, useId } from 'reka-ui'
import { useI18n } from 'vue-i18n'

defineOptions({
  inheritAttrs: false,
})

export interface UiCommandInputProps {
  modelValue?: string
  placeholder?: string
  label?: string
  autoFocus?: boolean
}

const props = withDefaults(defineProps<UiCommandInputProps>(), {
  modelValue: undefined,
  placeholder: undefined,
  label: undefined,
  autoFocus: false,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedPlaceholder = computed(
  () => props.placeholder ?? t('ui.commandInput.placeholder'),
)
const resolvedLabel = computed(() => props.label ?? t('ui.commandInput.label'))

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const rootContext = injectComboboxRootContext()
rootContext.contentId ||= useId(undefined, 'reka-command-content')
</script>

<template>
  <div class="flex items-center border-b border-border px-3">
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      class="mr-2 h-4 w-4 shrink-0 text-muted-foreground"
      aria-hidden="true"
    >
      <circle cx="11" cy="11" r="8" />
      <path d="m21 21-4.3-4.3" />
    </svg>
    <ComboboxInput
      :model-value="modelValue"
      :placeholder="resolvedPlaceholder"
      :aria-label="resolvedLabel"
      :auto-focus="autoFocus"
      v-bind="$attrs"
      class="flex h-11 w-full min-w-0 bg-transparent py-3 text-sm outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50"
      @update:model-value="emit('update:modelValue', $event)"
    />
  </div>
</template>
