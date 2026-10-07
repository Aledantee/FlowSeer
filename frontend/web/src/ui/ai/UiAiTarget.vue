<script setup lang="ts">
import { useTemplateRef } from 'vue'
import { Primitive } from 'reka-ui'
import type { UiAiEmits, UiAiProps } from './context'
import { useAiOrigin } from './useAiOrigin'
import { useAiTarget } from './useAiTarget'

// Makes native markup addressable without adding an element of its own. With
// `as` it renders that tag, such as `li` or `section`. With `asChild` it
// merges its attributes and listeners into the single child it is given, such
// as a link component, so table rows, list items, and links keep their
// semantics and layout.
export interface UiAiTargetProps extends UiAiProps {
  as?: string
  asChild?: boolean
}

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<UiAiTargetProps>(), {
  as: 'div',
  asChild: false,
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
  <Primitive ref="anchor" v-bind="$attrs" :as="as" :as-child="asChild">
    <slot />
  </Primitive>
</template>
