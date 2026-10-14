<script setup lang="ts">
import { useTemplateRef } from 'vue'
import { ComboboxGroup, ComboboxLabel } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiCommandGroupProps extends UiAiProps {
  heading?: string
}

const props = withDefaults(defineProps<UiCommandGroupProps>(), {
  heading: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const group = useTemplateRef('group')
useAiTarget(group, () => props.ai)
useAiOrigin(
  group,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
</script>

<template>
  <ComboboxGroup
    ref="group"
    class="overflow-hidden p-1 text-foreground [&_[data-reka-combobox-label]]:px-2 [&_[data-reka-combobox-label]]:py-1.5 [&_[data-reka-combobox-label]]:font-mono [&_[data-reka-combobox-label]]:text-2xs [&_[data-reka-combobox-label]]:font-semibold [&_[data-reka-combobox-label]]:uppercase [&_[data-reka-combobox-label]]:tracking-wide [&_[data-reka-combobox-label]]:text-muted-foreground"
  >
    <ComboboxLabel v-if="heading" as="div">
      {{ heading }}
    </ComboboxLabel>
    <slot />
  </ComboboxGroup>
</template>
