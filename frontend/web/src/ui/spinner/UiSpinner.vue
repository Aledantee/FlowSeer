<script setup lang="ts">
import { tv } from 'tailwind-variants'

export interface UiSpinnerProps {
  size?: 'sm' | 'md' | 'lg'
}

withDefaults(defineProps<UiSpinnerProps>(), {
  size: 'md',
})

const spinnerVariants = tv({
  base: 'animate-spin text-current shrink-0',
  variants: {
    size: {
      sm: 'h-3.5 w-3.5',
      md: 'h-4 w-4',
      lg: 'h-5 w-5',
    },
  },
  defaultVariants: {
    size: 'md',
  },
})
</script>

<template>
  <svg
    :class="spinnerVariants({ size })"
    xmlns="http://www.w3.org/2000/svg"
    fill="none"
    viewBox="0 0 24 24"
    aria-hidden="true"
  >
    <circle
      class="opacity-25"
      cx="12"
      cy="12"
      r="10"
      stroke="currentColor"
      stroke-width="4"
    />
    <path
      class="opacity-75"
      fill="currentColor"
      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
    />
  </svg>
</template>
