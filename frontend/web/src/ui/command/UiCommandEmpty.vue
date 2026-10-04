<script setup lang="ts">
import { computed } from 'vue'
import { ComboboxEmpty } from 'reka-ui'
import { useI18n } from 'vue-i18n'

export interface UiCommandEmptyProps {
  text?: string
}

const props = defineProps<UiCommandEmptyProps>()

const { t } = useI18n({ useScope: 'global' })
const resolvedText = computed(() => props.text ?? t('ui.commandEmpty.text'))
</script>

<template>
  <ComboboxEmpty class="py-6 text-center text-sm text-muted-foreground">
    <slot>{{ resolvedText }}</slot>
  </ComboboxEmpty>
</template>
