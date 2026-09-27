<script setup lang="ts">
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  VisuallyHidden,
} from 'reka-ui'
import UiCommand from './UiCommand.vue'

defineOptions({
  inheritAttrs: false,
})

export interface UiCommandDialogProps {
  open?: boolean
  defaultOpen?: boolean
  ignoreFilter?: boolean
}

withDefaults(defineProps<UiCommandDialogProps>(), {
  open: undefined,
  defaultOpen: false,
  ignoreFilter: false,
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()
</script>

<template>
  <DialogRoot
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <DialogPortal>
      <DialogOverlay class="bg-overlay fixed inset-0 z-50 backdrop-blur-xs" />
      <DialogContent
        class="bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-50 p-0 w-full max-w-xl overflow-hidden focus:outline-none"
        @keydown.escape="emit('update:open', false)"
      >
        <VisuallyHidden as-child>
          <DialogTitle>Command Palette</DialogTitle>
        </VisuallyHidden>
        <VisuallyHidden as-child>
          <DialogDescription>Search and command palette</DialogDescription>
        </VisuallyHidden>
        <UiCommand :ignore-filter="ignoreFilter" v-bind="$attrs">
          <slot />
        </UiCommand>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
