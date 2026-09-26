<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ScrollAreaCorner,
  ScrollAreaRoot,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaViewport,
} from 'reka-ui'

// Our scrollbar: native scrolling with the browser's bar hidden and a thin
// overlay thumb drawn in its place, so every scrolling region, the page
// included, looks the same in every browser and theme.
const props = withDefaults(
  defineProps<{
    // Which axes scroll; the other axis clips.
    axis?: 'y' | 'x' | 'both'
    // Classes for the scrolling element itself, where padding and scroll
    // padding belong.
    viewportClass?: unknown
    label?: string
  }>(),
  { axis: 'y', viewportClass: undefined, label: undefined },
)
const viewport = ref<InstanceType<typeof ScrollAreaViewport>>()
const vertical = computed(() => props.axis !== 'x')
const horizontal = computed(() => props.axis !== 'y')
// The scrolling element, for callers that scroll it or read its position.
const element = computed(() => viewport.value?.viewportElement)
defineExpose({ element })
</script>

<template>
  <ScrollAreaRoot class="scroll-area" type="hover" :scroll-hide-delay="700">
    <ScrollAreaViewport
      ref="viewport"
      :class="['scroll-viewport', viewportClass]"
      :aria-label="label"
    >
      <slot />
    </ScrollAreaViewport>
    <ScrollAreaScrollbar
      v-if="vertical"
      class="scroll-bar"
      orientation="vertical"
    >
      <ScrollAreaThumb class="scroll-thumb" />
    </ScrollAreaScrollbar>
    <ScrollAreaScrollbar
      v-if="horizontal"
      class="scroll-bar"
      orientation="horizontal"
    >
      <ScrollAreaThumb class="scroll-thumb" />
    </ScrollAreaScrollbar>
    <ScrollAreaCorner v-if="vertical && horizontal" />
  </ScrollAreaRoot>
</template>
