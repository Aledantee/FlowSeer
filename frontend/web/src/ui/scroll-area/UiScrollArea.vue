<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ScrollAreaCorner,
  ScrollAreaRoot,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaViewport,
} from 'reka-ui'

export interface UiScrollAreaProps {
  axis?: 'y' | 'x' | 'both'
  viewportClass?: unknown
  label?: string
  type?: 'auto' | 'always' | 'scroll' | 'hover'
}

const props = withDefaults(defineProps<UiScrollAreaProps>(), {
  axis: 'y',
  viewportClass: undefined,
  label: undefined,
  type: 'hover',
})

const viewport = ref<InstanceType<typeof ScrollAreaViewport>>()
const vertical = computed(() => props.axis !== 'x')
const horizontal = computed(() => props.axis !== 'y')

const element = computed(() => viewport.value?.viewportElement)
defineExpose({ element })
</script>

<template>
  <ScrollAreaRoot
    class="relative overflow-hidden"
    :type="type"
    :scroll-hide-delay="700"
  >
    <ScrollAreaViewport
      ref="viewport"
      :class="['w-full h-full rounded-[inherit]', viewportClass]"
      :aria-label="label"
    >
      <slot />
    </ScrollAreaViewport>
    <ScrollAreaScrollbar
      v-if="vertical"
      orientation="vertical"
      class="flex select-none touch-none p-0.5 transition-colors duration-140 ease-out hover:bg-hover data-[orientation=vertical]:w-2.5 data-[orientation=horizontal]:flex-col data-[orientation=horizontal]:h-2.5"
    >
      <ScrollAreaThumb
        class="relative flex-1 rounded-full bg-border hover:bg-muted-foreground transition-colors"
      />
    </ScrollAreaScrollbar>
    <ScrollAreaScrollbar
      v-if="horizontal"
      orientation="horizontal"
      class="flex select-none touch-none p-0.5 transition-colors duration-140 ease-out hover:bg-hover data-[orientation=vertical]:w-2.5 data-[orientation=horizontal]:flex-col data-[orientation=horizontal]:h-2.5"
    >
      <ScrollAreaThumb
        class="relative flex-1 rounded-full bg-border hover:bg-muted-foreground transition-colors"
      />
    </ScrollAreaScrollbar>
    <ScrollAreaCorner v-if="vertical && horizontal" />
  </ScrollAreaRoot>
</template>
