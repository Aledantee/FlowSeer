<script setup lang="ts">
import { Separator } from 'reka-ui'

export interface UiSeparatorProps {
  orientation?: 'horizontal' | 'vertical'
  decorative?: boolean
}

withDefaults(defineProps<UiSeparatorProps>(), {
  orientation: 'horizontal',
  decorative: false,
})
</script>

<template>
  <Separator
    :orientation="orientation"
    :decorative="decorative"
    :class="[
      'bg-border shrink-0',
      orientation === 'vertical' ? 'h-full w-[1px]' : 'h-[1px] w-full',
    ]"
  />
</template>
