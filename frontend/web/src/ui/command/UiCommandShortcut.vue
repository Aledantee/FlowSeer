<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export type UiCommandShortcutProps = UiAiProps

const props = withDefaults(defineProps<UiCommandShortcutProps>(), {
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const shortcut = useTemplateRef('shortcut')
useAiTarget(shortcut, () => props.ai)
useAiOrigin(
  shortcut,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
</script>

<template>
  <span
    ref="shortcut"
    class="ml-auto text-xs tracking-widest text-muted-foreground"
  >
    <slot />
  </span>
</template>
