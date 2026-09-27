<script setup lang="ts">
import { computed, provide, ref } from 'vue'
import WorkspacePage from '../WorkspacePage.vue'
import { UiScrollArea } from '../ui'
import { pageContext } from './page'
import type { PageContext } from './page'
const props = defineProps<{ context: PageContext }>()
provide(pageContext, props.context)
const scroller = ref<InstanceType<typeof UiScrollArea>>()
// The element that scrolls the page.
const pane = computed(() => scroller.value?.element)
// Each page, and each device page, animates in as its own view; scope
// changes within a page do not.
const key = computed(() =>
  props.context.view.value === 'device'
    ? `device-${props.context.deviceId.value ?? ''}`
    : props.context.view.value,
)
// The view on screen, which trails the location while the previous page
// animates out.
const displayed = ref(props.context.view.value)
defineExpose({ pane })
</script>

<template>
  <div :class="['pane', { 'canvas-view': displayed === 'topology' }]">
    <slot />
    <UiScrollArea
      ref="scroller"
      class="h-full"
      viewport-class="pane-scroll"
      label="Page"
    >
      <Transition
        name="page"
        mode="out-in"
        @after-leave="pane?.scrollTo({ top: 0 })"
        @before-enter="displayed = context.view.value"
      >
        <div :key="key" class="page">
          <WorkspacePage />
        </div>
      </Transition>
    </UiScrollArea>
  </div>
</template>

<style scoped>
.page-enter-active {
  transition:
    opacity 200ms ease,
    transform 200ms ease;
}
.page-leave-active {
  transition: opacity 110ms ease;
}
.page-enter-from {
  opacity: 0;
  transform: translateY(6px);
}
.page-leave-to {
  opacity: 0;
}
@media (prefers-reduced-motion: reduce) {
  .page-enter-active,
  .page-leave-active {
    transition: none;
  }
  .page-enter-from {
    transform: none;
  }
}
</style>
