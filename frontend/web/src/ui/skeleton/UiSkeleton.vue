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
  const classes: string[] = []
  if (props.width === undefined) {
    classes.push(props.variant === 'circular' ? 'w-10' : 'w-full')
  }
  if (props.height === undefined) {
    switch (props.variant) {
      case 'circular':
        classes.push('h-10')
        break
      case 'rectangular':
        classes.push('h-24')
        break
      case 'text':
      default:
        classes.push('h-4')
        break
    }
  }
  return classes.join(' ')
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
