<script setup lang="ts">
import { nextTick, onMounted, provide, ref, watch } from 'vue'
import ThemeSwitcher from '../components/ThemeSwitcher.vue'
import LocaleSwitcher from '../components/LocaleSwitcher.vue'
import { UiMotion, useMotionFeedback } from '../ui'
import {
  createFrame,
  frameContext,
  frameMove,
  FRAME_MOVE_SECONDS,
} from './frame'

const frame = createFrame()
provide(frameContext, frame)
const main = ref<HTMLElement | null>(null)
const { play, reduced } = useMotionFeedback()

watch(
  frame.sidebar,
  async (mode, previous, onCleanup) => {
    if (
      !frame.mainElement.value ||
      (mode === 'login') === (previous === 'login')
    )
      return
    if (window.matchMedia('(min-width: 801px)').matches && !reduced.value)
      frame.moved()
    let cancelled = false
    onCleanup(() => {
      cancelled = true
    })
    // Deferred Teleports fill the arriving regions in the navigation's render.
    await nextTick()
    if (cancelled) return
    const regions = [
      frame.sidebarElement.value?.querySelector('#frame-sidebar'),
      ...['topbar', 'topbar-tools', 'page'].map((region) =>
        frame.mainElement.value?.querySelector(`#frame-${region}`),
      ),
    ]
    for (const region of regions) {
      const targets = [...(region?.children ?? [])]
      for (const child of targets) {
        if (!(child instanceof HTMLElement)) continue
        const display = getComputedStyle(child).display
        // Contents wrappers paint through their descendants and have no box to fade.
        if (display === 'contents') targets.push(...child.children)
        else if (display !== 'none')
          play(child, { opacity: [0, 1] }, FRAME_MOVE_SECONDS)
      }
    }
  },
  { flush: 'sync' },
)

onMounted(() => {
  frame.mainElement.value = main.value
})
</script>

<template>
  <div
    class="shell relative brand-glow max-[801px]:flex-col"
    :class="{
      'sidebar-login': frame.sidebar.value === 'login',
      'sidebar-collapsed': frame.sidebar.value === 'collapsed',
    }"
  >
    <div
      class="frame-glass pointer-events-none absolute inset-0 bg-glass backdrop-blur-2xl backdrop-saturate-150"
      aria-hidden="true"
    ></div>
    <div id="frame-skip"></div>
    <aside
      :id="frame.sidebar.value === 'login' ? undefined : 'workspace-sidebar'"
      :ref="
        (element) =>
          (frame.sidebarElement.value = element as HTMLElement | null)
      "
      class="sidebar max-[800px]:p-[16px_20px_8px] max-[560px]:p-[14px_14px_6px]"
    >
      <div id="frame-sidebar"></div>
    </aside>
    <div ref="main" class="main-shell">
      <header
        class="topbar sticky top-0 z-(--z-sticky) flex items-center min-h-[54px] px-6 py-2.5 max-[1150px]:px-6 max-[800px]:px-5 max-[651px]:flex-wrap max-[651px]:justify-end max-[651px]:pt-1.5 max-[651px]:pb-2.5 max-[651px]:gap-y-1 max-[560px]:min-h-[50px] max-[560px]:px-3.5"
      >
        <UiMotion
          id="frame-topbar"
          as="div"
          layout="position"
          :transition="frameMove"
          :layout-dependency="frame.layoutDependency.value"
          class="min-w-0 flex-1 empty:hidden max-[651px]:order-last max-[651px]:basis-full"
        ></UiMotion>
        <div id="frame-topbar-tools" class="contents"></div>
        <div
          class="flex items-center gap-1.5 shrink-0 ml-auto max-[651px]:ml-0"
        >
          <ThemeSwitcher />
          <LocaleSwitcher />
        </div>
      </header>
      <UiMotion
        id="frame-page"
        as="div"
        layout="position"
        :transition="frameMove"
        :layout-dependency="frame.layoutDependency.value"
        class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-tl-[18px] border-t border-l border-chrome-border bg-glass-panel max-[800px]:rounded-tl-none max-[800px]:border-l-0"
      ></UiMotion>
    </div>
    <slot />
  </div>
</template>

<style scoped>
#frame-skip,
#frame-sidebar {
  display: contents;
}
</style>
