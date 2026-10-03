<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

export interface UiBreadcrumbSeparatorProps {
  as?: string
  separator?: string
}

const props = withDefaults(defineProps<UiBreadcrumbSeparatorProps>(), {
  as: 'li',
  separator: undefined,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedSeparator = computed(
  () => props.separator ?? t('ui.breadcrumbSeparator.separator'),
)
</script>

<template>
  <component
    :is="as"
    role="presentation"
    aria-hidden="true"
    class="text-muted-foreground select-none"
  >
    <slot>{{ resolvedSeparator }}</slot>
  </component>
</template>
