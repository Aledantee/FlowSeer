<script setup lang="ts">
import { watch } from 'vue'
import { ToastProvider, ToastViewport } from 'reka-ui'
import UiToast from './UiToast.vue'
import { useToast } from './useToast'

export interface UiToastProviderProps {
  duration?: number
}

withDefaults(defineProps<UiToastProviderProps>(), {
  duration: 5000,
})

const { toasts, dismiss, remove } = useToast()

watch(
  toasts,
  () => {
    for (const item of [...toasts.value]) {
      if (!item.open) {
        const el = document.querySelector(`[data-toast-id="${item.id}"]`)
        const animName = el ? window.getComputedStyle(el).animationName : 'none'
        if (!animName || animName === 'none') {
          remove(item.id)
        }
      }
    }
  },
  { deep: true, flush: 'sync' },
)
</script>

<template>
  <ToastProvider :duration="duration">
    <slot />
    <UiToast
      v-for="item in toasts"
      :key="item.id"
      :data-toast-id="item.id"
      :open="item.open"
      :title="item.title"
      :description="item.description"
      :variant="item.variant"
      :duration="item.duration"
      :action-text="item.action?.label"
      :action-alt-text="item.action?.altText || item.action?.label"
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
      class="fixed bottom-0 right-0 z-(--z-toast) flex flex-col p-4 gap-2 w-full max-w-[420px] pointer-events-none"
    />
  </ToastProvider>
</template>
