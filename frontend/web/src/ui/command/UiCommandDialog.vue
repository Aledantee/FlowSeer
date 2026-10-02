<script setup lang="ts">
import { computed } from 'vue'
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  VisuallyHidden,
} from 'reka-ui'
import { useI18n } from 'vue-i18n'
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
  title?: string
  description?: string
}

const props = withDefaults(defineProps<UiCommandDialogProps>(), {
  open: undefined,
  defaultOpen: false,
  ignoreFilter: false,
  modelValue: '',
  highlightedValue: '',
  title: undefined,
  description: undefined,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedTitle = computed(() => props.title ?? t('ui.commandDialog.title'))
const resolvedDescription = computed(
  () => props.description ?? t('ui.commandDialog.description'),
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'update:modelValue', value: string): void
  (e: 'update:highlightedValue', value: string): void
  (e: 'highlight', value: string): void
  (e: 'closeAutoFocus', event: Event): void
}>()
</script>

<template>
  <DialogRoot
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <DialogPortal>
      <DialogOverlay
        class="bg-overlay fixed inset-0 z-(--z-overlay) backdrop-blur-xs data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out"
      />
      <DialogContent
        class="bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-(--z-overlay) p-0 w-full max-w-xl overflow-hidden focus:outline-none data-[state=closed]:animate-fade-out"
        @keydown.escape="emit('update:open', false)"
        @close-auto-focus="emit('closeAutoFocus', $event)"
      >
        <VisuallyHidden as-child>
          <DialogTitle>{{ resolvedTitle }}</DialogTitle>
        </VisuallyHidden>
        <VisuallyHidden as-child>
          <DialogDescription>{{ resolvedDescription }}</DialogDescription>
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
