<script setup lang="ts">
import { computed } from 'vue'
import {
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogRoot,
  AlertDialogTitle,
  AlertDialogTrigger,
} from 'reka-ui'
import { useI18n } from 'vue-i18n'
import UiButton from '../button/UiButton.vue'

export interface UiAlertDialogProps {
  open?: boolean
  defaultOpen?: boolean
  title: string
  description: string
  confirmText?: string
  cancelText?: string
  destructive?: boolean
}

const props = withDefaults(defineProps<UiAlertDialogProps>(), {
  open: undefined,
  defaultOpen: false,
  confirmText: undefined,
  cancelText: undefined,
  destructive: false,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedConfirmText = computed(
  () => props.confirmText ?? t('ui.alertDialog.confirmText'),
)
const resolvedCancelText = computed(
  () => props.cancelText ?? t('ui.alertDialog.cancelText'),
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'confirm'): void
  (e: 'cancel'): void
  (e: 'closeAutoFocus', event: Event): void
}>()
</script>

<template>
  <AlertDialogRoot
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <AlertDialogTrigger v-if="$slots.trigger" as-child>
      <slot name="trigger" />
    </AlertDialogTrigger>
    <AlertDialogPortal>
      <AlertDialogOverlay
        class="bg-overlay fixed inset-0 z-(--z-overlay) backdrop-blur-xs data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out"
      />
      <AlertDialogContent
        :disable-outside-pointer-events="true"
        class="bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-(--z-overlay) p-6 w-full max-w-md focus:outline-none data-[state=open]:animate-dialog-in data-[state=closed]:animate-dialog-out motion-reduce:data-[state=open]:animate-dialog-fade-in motion-reduce:data-[state=closed]:animate-fade-out"
        @close-auto-focus="emit('closeAutoFocus', $event)"
      >
        <div class="flex flex-col gap-2 mb-6">
          <AlertDialogTitle class="text-lg font-semibold text-foreground">
            {{ title }}
          </AlertDialogTitle>
          <AlertDialogDescription class="text-sm text-muted-foreground">
            {{ description }}
          </AlertDialogDescription>
        </div>
        <div class="flex items-center justify-end gap-3">
          <AlertDialogCancel as-child>
            <UiButton variant="secondary" @click="emit('cancel')">
              {{ resolvedCancelText }}
            </UiButton>
          </AlertDialogCancel>
          <AlertDialogAction as-child>
            <UiButton
              :variant="destructive ? 'danger' : 'primary'"
              @click="emit('confirm')"
            >
              {{ resolvedConfirmText }}
            </UiButton>
          </AlertDialogAction>
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>
