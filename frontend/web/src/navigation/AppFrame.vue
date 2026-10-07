<script setup lang="ts">
import { onMounted, provide, ref } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { UiMotion } from '../ui'
import { createFrame, frameContext } from './frame'

const frame = createFrame()
provide(frameContext, frame)
const main = ref<ComponentPublicInstance | null>(null)

onMounted(() => {
  frame.mainElement.value =
    main.value?.$el instanceof HTMLElement ? main.value.$el : null
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
      <div id="frame-topbar"></div>
      <div
        id="frame-page"
        class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-tl-[18px] border-t border-l border-chrome-border max-[800px]:rounded-tl-none max-[800px]:border-l-0"
      ></div>
    </UiMotion>
    <slot />
  </div>
</template>

<style scoped>
#frame-skip,
#frame-sidebar {
  display: contents;
}

#frame-topbar {
  min-height: 54px;
}
</style>
