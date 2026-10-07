<script setup lang="ts">
import { onMounted, onUnmounted, provide, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { UiMotion } from '../ui'
import { createFrame, frameContext } from './frame'

const frame = createFrame()
provide(frameContext, frame)
const main = ref<ComponentPublicInstance | null>(null)
const topbar = ref<HTMLElement | null>(null)
let observer: ResizeObserver | undefined

onMounted(() => {
  frame.mainElement.value =
    main.value?.$el instanceof HTMLElement ? main.value.$el : null
  if (!topbar.value) return
  observer = new ResizeObserver(() => {
    const height = topbar.value?.offsetHeight || 54
    frame.topbarHeight.value = height
    frame.mainElement.value?.style.setProperty('--topbar-height', `${height}px`)
  })
  observer.observe(topbar.value)
})
onUnmounted(() => observer?.disconnect())

watch(frame.sidebar, (mode) => {
  if (mode === 'login') frame.topbarHeight.value = 54
})
</script>

<template>
  <div
    class="shell brand-glow max-[800px]:flex-col"
    :class="{ 'sidebar-collapsed': frame.sidebar.value === 'collapsed' }"
  >
    <div id="frame-skip"></div>
    <aside
      :id="frame.sidebar.value === 'login' ? undefined : 'workspace-sidebar'"
      :ref="
        (element) =>
          (frame.sidebarElement.value = element as HTMLElement | null)
      "
      class="sidebar max-[800px]:p-[16px_20px_8px] max-[560px]:p-[14px_14px_6px]"
      :class="
        frame.sidebar.value === 'login'
          ? 'login-sidebar login-glass border-r border-chrome-border max-[800px]:border-r-0 max-[800px]:border-b bg-card/45 text-foreground backdrop-blur-2xl backdrop-saturate-150 max-[800px]:pb-6'
          : ''
      "
    >
      <div id="frame-sidebar"></div>
    </aside>
    <UiMotion
      ref="main"
      as="div"
      layout="position"
      :layout-dependency="frame.layoutDependency.value"
      class="main-shell"
    >
      <span
        v-if="frame.sidebar.value !== 'login'"
        class="main-notch"
        aria-hidden="true"
      ></span>
      <div id="frame-topbar" ref="topbar"></div>
      <div id="frame-page"></div>
    </UiMotion>
    <slot />
  </div>
</template>

<style scoped>
#frame-skip,
#frame-sidebar,
#frame-page {
  display: contents;
}

#frame-topbar {
  min-height: 54px;
}
</style>
