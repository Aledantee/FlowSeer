<script setup lang="ts">
import { useTemplateRef } from 'vue'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiCardProps extends UiAiProps {
  as?: 'div' | 'article' | 'section'
}

const props = withDefaults(defineProps<UiCardProps>(), {
  as: 'div',
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
    class="ui-card relative bg-card border border-border rounded-panel p-4 flex flex-col gap-3"
  >
    <header v-if="$slots.header" class="flex items-center justify-between">
      <slot name="header" />
    </header>
    <div v-if="$slots.default" class="flex-1">
      <slot />
    </div>
    <footer v-if="$slots.footer" class="mt-auto pt-2 border-t border-border">
      <slot name="footer" />
    </footer>
  </component>
</template>

<style scoped>
.ui-card::before,
.ui-card::after {
  position: absolute;
  width: 7px;
  height: 7px;
  border-color: var(--graph-edge);
  content: '';
  opacity: 0.65;
  pointer-events: none;
}

.ui-card::before {
  inset-block-start: -1px;
  inset-inline-start: -1px;
  border-block-start: 1px solid;
  border-inline-start: 1px solid;
}

.ui-card::after {
  inset-inline-end: -1px;
  inset-block-end: -1px;
  border-inline-end: 1px solid;
  border-block-end: 1px solid;
}
</style>
