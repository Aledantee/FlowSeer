<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiBreadcrumbPageProps extends UiAiProps {
  as?: string
}

const props = withDefaults(defineProps<UiBreadcrumbPageProps>(), {
  as: 'span',
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const anchor = useTemplateRef('anchor')
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
</script>

<template>
  <component
    :is="as"
    ref="anchor"
    role="link"
    aria-disabled="true"
    aria-current="page"
    class="font-medium text-foreground"
  >
    <slot />
  </component>
</template>
