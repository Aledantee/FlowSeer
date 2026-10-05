<script setup lang="ts">
import { useTemplateRef } from 'vue'
import { ComboboxRoot } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiCommandProps extends UiAiProps {
  modelValue?: string
  highlightedValue?: string
  ignoreFilter?: boolean
}

const props = withDefaults(defineProps<UiCommandProps>(), {
  modelValue: '',
  highlightedValue: '',
  ignoreFilter: false,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<
  UiAiEmits & {
    (e: 'update:modelValue', value: string): void
    (e: 'update:highlightedValue', value: string): void
    (e: 'highlight', value: string): void
  }
>()

const root = useTemplateRef('root')
// Reka's combobox root renders a fragment, so its `$el` is the start marker
// and the root element follows it, as in Reka's own `useForwardExpose`.
const anchor = () => {
  const start = (root.value as { $el?: unknown } | null)?.$el
  if (start instanceof Element) return start
  return start instanceof Text || start instanceof Comment
    ? start.nextElementSibling
    : null
}
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

function onHighlight(item: unknown) {
  let val = ''
  if (item && typeof item === 'object' && 'value' in item) {
    val = String((item as { value: unknown }).value ?? '')
  } else if (typeof item === 'string') {
    val = item
  }
  emit('update:highlightedValue', val)
  emit('update:modelValue', val)
  emit('highlight', val)
}
</script>

<template>
  <ComboboxRoot
    ref="root"
    :open="true"
    :model-value="modelValue"
    :ignore-filter="ignoreFilter"
    class="bg-popover text-foreground flex h-full w-full flex-col overflow-hidden rounded-panel"
    @update:model-value="emit('update:modelValue', $event as string)"
    @highlight="onHighlight"
  >
    <slot />
  </ComboboxRoot>
</template>
