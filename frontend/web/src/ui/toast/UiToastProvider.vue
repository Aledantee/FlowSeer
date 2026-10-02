<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ToastProvider, ToastViewport } from 'reka-ui'
import UiToast from './UiToast.vue'
import { useToast } from './useToast'

export interface UiToastProviderProps {
  duration?: number
  announcementLabel?: string
  viewportLabel?: string | ((hotkey: string) => string)
}

const props = withDefaults(defineProps<UiToastProviderProps>(), {
  duration: 5000,
  announcementLabel: undefined,
  viewportLabel: undefined,
})

const { t } = useI18n({ useScope: 'global' })

const resolvedAnnouncementLabel = computed(
  () => props.announcementLabel ?? t('ui.toastProvider.announcementLabel'),
)

const resolvedViewportLabel = computed(() => {
  return (hotkey: string) => {
    if (typeof props.viewportLabel === 'function') {
      return props.viewportLabel(hotkey)
    }
    if (typeof props.viewportLabel === 'string') {
      return props.viewportLabel.replace('{hotkey}', hotkey)
    }
    return t('ui.toastProvider.viewportLabel', { hotkey })
  }
})

const { toasts, dismiss, remove } = useToast()
</script>

<template>
  <ToastProvider :duration="duration" :label="resolvedAnnouncementLabel">
    <slot />
    <UiToast
      v-for="item in toasts"
      :key="item.id"
      :open="item.open"
      :title="item.title"
      :description="item.description"
      :variant="item.variant"
      :duration="item.duration"
      :action-text="item.action?.label"
      :action-alt-text="item.action?.altText ?? item.action?.label"
      @update:open="
        (val) => {
          if (!val) {
            dismiss(item.id)
          }
        }
      "
      @closed="remove(item.id)"
      @action="item.action?.onClick?.()"
    />
    <ToastViewport
      :label="resolvedViewportLabel"
      class="fixed bottom-0 right-0 z-(--z-toast) flex flex-col p-4 gap-2 w-full max-w-[420px] pointer-events-none"
    />
  </ToastProvider>
</template>
