<script setup lang="ts">
import { computed, ref, watch, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ToastAction,
  ToastClose,
  ToastDescription,
  ToastRoot,
  ToastTitle,
} from 'reka-ui'
import { tv } from 'tailwind-variants'
import type { ToastVariant } from './useToast'
import UiButton from '../button/UiButton.vue'

export interface UiToastProps {
  open?: boolean
  defaultOpen?: boolean
  title?: string
  description?: string
  variant?: ToastVariant
  duration?: number
  actionText?: string
  actionAltText?: string
  closeLabel?: string
}

const props = withDefaults(defineProps<UiToastProps>(), {
  open: undefined,
  defaultOpen: true,
  title: undefined,
  description: undefined,
  variant: 'default',
  duration: undefined,
  actionText: undefined,
  actionAltText: undefined,
  closeLabel: undefined,
})

const { t } = useI18n({ useScope: 'global' })

const resolvedActionAltText = computed(
  () => props.actionAltText ?? t('ui.toast.actionAltText'),
)
const resolvedCloseLabel = computed(
  () => props.closeLabel ?? t('ui.toast.closeLabel'),
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'action'): void
  (e: 'closed'): void
}>()

const rootRef = ref<ComponentPublicInstance | null>(null)
const hasClosed = ref(false)

function getRootElement(): HTMLElement | null {
  const target = rootRef.value as
    (ComponentPublicInstance & { currentElement?: HTMLElement }) | null
  const el = target?.currentElement || target?.$el
  return el instanceof HTMLElement ? el : null
}

function checkClosed() {
  if (hasClosed.value) return
  const el = getRootElement()
  const animName = el ? window.getComputedStyle(el).animationName : 'none'
  if (!animName || animName === 'none') {
    hasClosed.value = true
    emit('closed')
  }
}

function handleUpdateOpen(val: boolean) {
  emit('update:open', val)
  if (!val) {
    checkClosed()
  }
}

watch(
  () => props.open,
  (val, oldVal) => {
    if (val === true) {
      hasClosed.value = false
    } else if (oldVal !== false && val === false) {
      checkClosed()
    }
  },
  { flush: 'sync' },
)

function handleAnimationEnd(event: AnimationEvent) {
  if (hasClosed.value || event.target !== event.currentTarget) return
  if (event.animationName === 'fade-out') {
    hasClosed.value = true
    emit('closed')
  }
}

const toastVariants = tv({
  base: 'pointer-events-auto bg-popover text-foreground border shadow-lg rounded-control p-4 flex items-center justify-between gap-4 data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out',
  variants: {
    variant: {
      default: 'border-border',
      success: 'border-success-border',
      warning: 'border-warning-border',
      danger: 'border-danger-border',
    },
  },
  defaultVariants: {
    variant: 'default',
  },
})
</script>

<template>
  <ToastRoot
    ref="rootRef"
    :open="open"
    :default-open="defaultOpen"
    :duration="duration"
    :class="toastVariants({ variant })"
    @update:open="handleUpdateOpen"
    @animationend="handleAnimationEnd"
  >
    <div class="flex flex-col gap-1">
      <ToastTitle
        v-if="title || $slots.title"
        class="text-sm font-semibold text-foreground"
      >
        <slot name="title">{{ title }}</slot>
      </ToastTitle>
      <ToastDescription
        v-if="description || $slots.description"
        class="text-xs text-muted-foreground"
      >
        <slot name="description">{{ description }}</slot>
      </ToastDescription>
    </div>
    <div class="flex items-center gap-2">
      <ToastAction
        v-if="actionText || $slots.action"
        as-child
        :alt-text="resolvedActionAltText"
      >
        <slot name="action">
          <UiButton size="sm" variant="secondary" @click="emit('action')">
            {{ actionText }}
          </UiButton>
        </slot>
      </ToastAction>
      <ToastClose
        class="rounded-xs p-1 text-muted-foreground hover:text-foreground transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        :aria-label="resolvedCloseLabel"
      >
        <svg
          width="14"
          height="14"
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
      </ToastClose>
    </div>
  </ToastRoot>
</template>
