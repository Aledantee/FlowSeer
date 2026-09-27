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
  modelValue?: string
  highlightedValue?: string
}

withDefaults(defineProps<UiCommandDialogProps>(), {
  open: undefined,
  defaultOpen: false,
  ignoreFilter: false,
  modelValue: '',
  highlightedValue: '',
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'update:modelValue', value: string): void
  (e: 'update:highlightedValue', value: string): void
  (e: 'highlight', value: string): void
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
        <UiCommand
          :model-value="modelValue"
          :highlighted-value="highlightedValue"
          :ignore-filter="ignoreFilter"
          v-bind="$attrs"
          @update:model-value="emit('update:modelValue', $event)"
          @update:highlighted-value="emit('update:highlightedValue', $event)"
          @highlight="emit('highlight', $event)"
        >
          <slot />
        </UiCommand>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
