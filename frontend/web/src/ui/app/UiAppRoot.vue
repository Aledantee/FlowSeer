<script setup lang="ts">
import { ConfigProvider, TooltipProvider } from 'reka-ui'

export interface UiAppRootProps {
  locale?: string
  dir?: 'ltr' | 'rtl'
  scrollBody?: boolean
}

withDefaults(defineProps<UiAppRootProps>(), {
  locale: 'en',
  dir: 'ltr',
  scrollBody: undefined,
})
</script>

<template>
  <ConfigProvider :locale="locale" :dir="dir" :scroll-body="scrollBody">
    <TooltipProvider :delay-duration="350" :skip-delay-duration="250">
      <slot />
    </TooltipProvider>
  </ConfigProvider>
</template>
