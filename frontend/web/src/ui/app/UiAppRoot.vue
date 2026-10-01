<script setup lang="ts">
import { ConfigProvider, TooltipProvider } from 'reka-ui'
import { UiMotionConfig } from '../motion'

const motionTransition = {
  duration: 0.14,
  ease: [0.2, 0, 0, 1] as [number, number, number, number],
}

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
      <UiMotionConfig reduced-motion="user" :transition="motionTransition">
        <slot />
      </UiMotionConfig>
    </TooltipProvider>
  </ConfigProvider>
</template>
