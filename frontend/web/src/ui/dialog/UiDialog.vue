<script setup lang="ts">
import { computed } from 'vue'
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
import { useI18n } from 'vue-i18n'
import { tv } from 'tailwind-variants'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { PopupAnchor } from '../popover/popupAnchor'

export interface UiDialogProps extends UiAiProps {
  open?: boolean
  defaultOpen?: boolean
  title?: string
  description?: string
  size?: 'sm' | 'md' | 'lg'
  side?: 'right'
  contentClass?: string
  fallbackTitle?: string
  fallbackDescription?: string
  closeLabel?: string
}

const props = withDefaults(defineProps<UiDialogProps>(), {
  open: undefined,
  defaultOpen: false,
  title: undefined,
  description: undefined,
  size: 'md',
  side: undefined,
  contentClass: undefined,
  fallbackTitle: undefined,
  fallbackDescription: undefined,
  closeLabel: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedFallbackTitle = computed(
  () => props.fallbackTitle ?? t('ui.dialog.fallbackTitle'),
)
const resolvedFallbackDescription = computed(
  () => props.fallbackDescription ?? t('ui.dialog.fallbackDescription'),
)
const resolvedCloseLabel = computed(
  () => props.closeLabel ?? t('ui.dialog.closeLabel'),
)

const emit = defineEmits<
  UiAiEmits & {
    (e: 'update:open', value: boolean): void
    (e: 'closeAutoFocus', event: Event): void
  }
>()

const dialogVariants = tv({
  base: 'bg-popover text-foreground border border-border shadow-lg z-(--z-overlay) focus:outline-none data-[state=open]:animate-dialog-in data-[state=closed]:animate-dialog-out motion-reduce:data-[state=open]:animate-dialog-fade-in motion-reduce:data-[state=closed]:animate-fade-out',
  variants: {
    side: {
      center:
        'rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 p-6 w-full',
      right:
        'fixed inset-y-0 right-0 h-full border-l border-y-0 border-r-0 rounded-none p-6 w-full overflow-y-auto',
    },
    size: {
      sm: 'max-w-sm',
      md: 'max-w-lg',
      lg: 'max-w-2xl',
    },
  },
  defaultVariants: {
    side: 'center',
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
        class="bg-overlay fixed inset-0 z-(--z-overlay) backdrop-blur-xs data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out"
      />
      <DialogContent
        :class="[
          dialogVariants({ side: side ?? 'center', size }),
          contentClass,
        ]"
        @close-auto-focus="emit('closeAutoFocus', $event)"
      >
        <PopupAnchor
          :ai="ai"
          :ai-origin="aiOrigin"
          @ai-origin-acknowledged="emit('aiOriginAcknowledged', $event)"
        />
        <VisuallyHidden v-if="!title && !$slots.title" as-child>
          <DialogTitle>{{ resolvedFallbackTitle }}</DialogTitle>
        </VisuallyHidden>
        <VisuallyHidden v-if="!description && !$slots.description" as-child>
          <DialogDescription>{{
            title || resolvedFallbackDescription
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
          :aria-label="resolvedCloseLabel"
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
