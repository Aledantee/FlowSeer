<script setup lang="ts">
import { injectComboboxRootContext } from 'reka-ui'
import { ListboxContent } from 'reka-ui'

export interface UiCommandListProps {
  label?: string
}

withDefaults(defineProps<UiCommandListProps>(), {
  label: 'Commands',
})

const rootContext = injectComboboxRootContext()
</script>

<template>
  <ListboxContent
    :id="rootContext.contentId"
    :aria-label="label"
    class="max-h-[300px] overflow-y-auto overflow-x-hidden p-1 focus:outline-none"
  >
    <slot />
  </ListboxContent>
</template>
