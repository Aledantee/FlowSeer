<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { ComboboxItemEmits } from 'reka-ui'
import { ComboboxItem, injectComboboxRootContext } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiCommandItemProps extends UiAiProps {
  value: string
  disabled?: boolean
}

const props = withDefaults(defineProps<UiCommandItemProps>(), {
  disabled: false,
  ai: undefined,
  aiOrigin: undefined,
})

type RekaSelectEvent = ComboboxItemEmits<string>['select'][0]

export interface UiCommandItemSelectEvent {
  value: string
  altKey: boolean
  ctrlKey: boolean
  metaKey: boolean
  shiftKey: boolean
}

const emit = defineEmits<
  UiAiEmits & {
    (e: 'select', event: UiCommandItemSelectEvent): void
  }
>()

const rootContext = injectComboboxRootContext()
const item = useTemplateRef('item')
const anchor = () => {
  void rootContext.filterState.value
  return item.value
}
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

function select(event: RekaSelectEvent) {
  const { originalEvent } = event.detail
  emit('select', {
    value: props.value,
    altKey: originalEvent.altKey,
    ctrlKey: originalEvent.ctrlKey,
    metaKey: originalEvent.metaKey,
    shiftKey: originalEvent.shiftKey,
  })
}
</script>

<template>
  <ComboboxItem
    ref="item"
    :value="value"
    :disabled="disabled"
    :data-command-value="value"
    class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
    @select="select"
  >
    <slot />
  </ComboboxItem>
</template>
