<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { ComboboxEmpty, injectComboboxRootContext } from 'reka-ui'
import { useI18n } from 'vue-i18n'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface UiCommandEmptyProps extends UiAiProps {
  text?: string
}

const props = withDefaults(defineProps<UiCommandEmptyProps>(), {
  text: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const { t } = useI18n({ useScope: 'global' })
const resolvedText = computed(() => props.text ?? t('ui.commandEmpty.text'))

const rootContext = injectComboboxRootContext()
const empty = useTemplateRef('empty')
const anchor = () => {
  void rootContext.filterState.value
  void rootContext.allItems.value.size
  return empty.value
}
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)
</script>

<template>
  <ComboboxEmpty
    ref="empty"
    class="py-6 text-center text-sm text-muted-foreground"
  >
    <slot>{{ resolvedText }}</slot>
  </ComboboxEmpty>
</template>
