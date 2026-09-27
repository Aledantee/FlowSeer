<script setup lang="ts">
import { computed } from 'vue'

export interface UiSkeletonProps {
  variant?: 'text' | 'circular' | 'rectangular'
  width?: string | number
  height?: string | number
}

const props = withDefaults(defineProps<UiSkeletonProps>(), {
  variant: 'text',
  width: undefined,
  height: undefined,
})

const variantClass = computed(() => {
  switch (props.variant) {
    case 'circular':
      return 'rounded-full'
    case 'rectangular':
    case 'text':
    default:
      return 'rounded-control'
  }
})

const defaultSizeClass = computed(() => {
  if (props.width || props.height) return ''
  switch (props.variant) {
    case 'circular':
      return 'h-10 w-10'
    case 'rectangular':
      return 'h-24 w-full'
    case 'text':
    default:
      return 'h-4 w-full'
  }
})

const inlineStyle = computed(() => {
  const style: Record<string, string> = {}
  if (props.width !== undefined) {
    style.width =
      typeof props.width === 'number' ? `${props.width}px` : props.width
  }
  if (props.height !== undefined) {
    style.height =
      typeof props.height === 'number' ? `${props.height}px` : props.height
  }
  return style
})
</script>

<template>
  <div
    role="presentation"
    aria-hidden="true"
    :class="[
      'bg-subtle animate-pulse motion-reduce:animate-none',
      variantClass,
      defaultSizeClass,
    ]"
    :style="inlineStyle"
  />
</template>
