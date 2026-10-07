<script setup lang="ts">
import { useTemplateRef } from 'vue'
import { Primitive } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiBreadcrumbLinkProps extends UiAiProps {
  asChild?: boolean
  as?: string
  href?: string
}

const props = withDefaults(defineProps<UiBreadcrumbLinkProps>(), {
  asChild: false,
  as: 'a',
  href: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const link = useTemplateRef('link')
useAiTarget(link, () => props.ai)
useAiOrigin(
  link,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
</script>

<template>
  <Primitive
    ref="link"
    :as="as"
    :as-child="asChild"
    :href="href"
    class="transition-colors hover:text-foreground cursor-pointer"
  >
    <slot />
  </Primitive>
</template>
