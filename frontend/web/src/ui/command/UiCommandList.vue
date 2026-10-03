<script setup lang="ts">
import { computed } from 'vue'
import { injectComboboxRootContext, ListboxContent } from 'reka-ui'
import { useI18n } from 'vue-i18n'

export interface UiCommandListProps {
  label?: string
}

const props = defineProps<UiCommandListProps>()

const { t } = useI18n({ useScope: 'global' })
const resolvedLabel = computed(() => props.label ?? t('ui.commandList.label'))

const rootContext = injectComboboxRootContext()
</script>

<template>
  <ListboxContent
    :id="rootContext.contentId"
    :aria-label="resolvedLabel"
    class="max-h-[300px] overflow-y-auto overflow-x-hidden p-1 focus:outline-none"
  >
    <slot />
  </ListboxContent>
</template>
