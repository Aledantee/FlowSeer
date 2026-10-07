<script setup lang="ts">
import { useTemplateRef } from 'vue'
import { DropdownMenuItem } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiDropdownMenuItemProps extends UiAiProps {
  disabled?: boolean
  textValue?: string
  asChild?: boolean
}

const props = withDefaults(defineProps<UiDropdownMenuItemProps>(), {
  disabled: false,
  textValue: undefined,
  asChild: false,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<
  UiAiEmits & {
    (e: 'select', event: Event): void
  }
>()

const item = useTemplateRef('item')
useAiTarget(item, () => props.ai)
useAiOrigin(
  item,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
</script>

<template>
  <DropdownMenuItem
    ref="item"
    :disabled="disabled"
    :text-value="textValue"
    :as-child="asChild"
    class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
    @select="emit('select', $event)"
  >
    <slot />
  </DropdownMenuItem>
</template>
