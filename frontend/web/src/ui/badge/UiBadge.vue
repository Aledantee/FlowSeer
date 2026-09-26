<script setup lang="ts">
import { tv } from 'tailwind-variants'

export interface UiBadgeProps {
  variant?:
    | 'default'
    | 'outline'
    | 'primary'
    | 'accent'
    | 'success'
    | 'warning'
    | 'danger'
    | 'info'
  size?: 'sm' | 'md'
}

withDefaults(defineProps<UiBadgeProps>(), {
  variant: 'default',
  size: 'md',
})

const badgeVariants = tv({
  base: 'inline-flex items-center gap-1.5 font-medium rounded-full whitespace-nowrap',
  variants: {
    variant: {
      default: 'bg-subtle text-foreground border border-border',
      outline: 'bg-transparent text-foreground border border-border',
      primary: 'bg-primary text-primary-foreground',
      accent: 'bg-accent text-accent-foreground',
      success:
        'bg-success-surface text-success-foreground border border-success-border',
      warning:
        'bg-warning-surface text-warning-foreground border border-warning-border',
      danger:
        'bg-danger-surface text-danger-foreground border border-danger-border',
      info: 'bg-info-surface text-info-foreground border border-info-border',
    },
    size: {
      sm: 'h-5 px-2 text-2xs',
      md: 'h-6 px-2.5 text-xs',
    },
  },
  defaultVariants: {
    variant: 'default',
    size: 'md',
  },
})
</script>

<template>
  <span :class="badgeVariants({ variant, size })">
    <slot />
  </span>
</template>
