<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ConfigProvider, TooltipProvider } from 'reka-ui'
import { UiMotionConfig } from '../motion'

const motionTransition = {
  duration: 0.14,
  ease: [0.2, 0, 0, 1] as [number, number, number, number],
}

export interface UiAppRootProps {
  dir?: 'ltr' | 'rtl'
  scrollBody?: boolean
}

withDefaults(defineProps<UiAppRootProps>(), {
  dir: 'ltr',
  scrollBody: undefined,
})

const { locale } = useI18n({ useScope: 'global' })
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
