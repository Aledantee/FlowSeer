<script setup lang="ts">
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
  VisuallyHidden,
} from 'reka-ui'
import { tv } from 'tailwind-variants'

export interface UiDialogProps {
  open?: boolean
  defaultOpen?: boolean
  title?: string
  description?: string
  size?: 'sm' | 'md' | 'lg'
}

withDefaults(defineProps<UiDialogProps>(), {
  open: undefined,
  defaultOpen: false,
  title: undefined,
  description: undefined,
  size: 'md',
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const dialogVariants = tv({
  base: 'bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-50 p-6 w-full focus:outline-none',
  variants: {
    size: {
      sm: 'max-w-sm',
      md: 'max-w-lg',
      lg: 'max-w-2xl',
    },
  },
  defaultVariants: {
    size: 'md',
  },
})
</script>

<template>
  <DialogRoot
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <DialogTrigger v-if="$slots.trigger" as-child>
      <slot name="trigger" />
    </DialogTrigger>
    <DialogPortal>
      <DialogOverlay
        class="bg-overlay fixed inset-0 z-50 backdrop-blur-xs transition-opacity duration-140 ease-out"
      />
      <DialogContent :class="dialogVariants({ size })">
        <VisuallyHidden v-if="!title && !$slots.title" as-child>
          <DialogTitle>Dialog</DialogTitle>
        </VisuallyHidden>
        <VisuallyHidden v-if="!description && !$slots.description" as-child>
          <DialogDescription>{{
            title || 'Dialog description'
          }}</DialogDescription>
        </VisuallyHidden>
        <div
          v-if="title || $slots.title || description || $slots.description"
          class="flex flex-col gap-1.5 mb-4"
        >
          <DialogTitle
            v-if="title || $slots.title"
            class="text-lg font-semibold text-foreground"
          >
            <slot name="title">{{ title }}</slot>
          </DialogTitle>
          <DialogDescription
            v-if="description || $slots.description"
            class="text-sm text-muted-foreground"
          >
            <slot name="description">{{ description }}</slot>
          </DialogDescription>
        </div>
        <slot />
        <div
          v-if="$slots.footer"
          class="mt-6 flex items-center justify-end gap-3"
        >
          <slot name="footer" />
        </div>
        <DialogClose
          class="absolute top-4 right-4 inline-flex items-center justify-center rounded-xs p-1 text-muted-foreground hover:text-foreground transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          aria-label="Close"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M18 6 6 18M6 6l12 12" />
          </svg>
        </DialogClose>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
